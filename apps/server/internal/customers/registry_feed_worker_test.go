package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the feed worker end to end (registry workers design D1–D5),
// driven through RunCycle against the DB harness with a fake transport serving
// pages recorded from the live API. Nothing here opens a socket and nothing
// waits on a real interval: a cycle is a method call.
//
// **The clock is the thing to get right here.** modtest.Start is
// 2026-09-12T12:00:00Z, and that is where a create hook's fetched_at lands. A
// feed entry is therefore dated AFTER it (feedEntryDate, feedEntryDateLater)
// and every test that runs a cycle over a matched customer advances the clock
// by afterTheFeed first — so a hint is newer than the record it lands on and
// older than the refresh that answers it, which is production's own ordering
// and the only ordering in which "hint > fetched_at means stale" says anything.
// Dating a fixture in 2026-09-21 instead, with the clock left at Start, makes
// every hint newer than every fetch forever: the failed-refresh test would pass
// without a worker and the successful-refresh assertion could never hold.
//
// The advance is two hours, not two weeks, because the harness's session idle
// timeout is 8h and its absolute session lifetime 24h — every test here reads
// its result back through the API afterwards.

const (
	// The two moments the fixtures' entries are published at, between
	// modtest.Start and Start+afterTheFeed.
	feedEntryDate      = "2026-09-12T12:30:00.000Z"
	feedEntryDateLater = "2026-09-12T13:00:00.000Z"
	// afterTheFeed is how far a test moves the clock before running a cycle.
	afterTheFeed = 2 * time.Hour
)

// brregEntityRegistryBody is 974760673's own record, for a test that needs two
// different companies on one feed page.
const brregEntityRegistryBody = `{
	"organisasjonsnummer": "974760673",
	"navn": "REGISTERENHETEN I BRØNNØYSUND",
	"organisasjonsform": {"kode": "ORGL", "beskrivelse": "Organisasjonsledd"},
	"naeringskode1": {"kode": "84.110", "beskrivelse": "Generell offentlig administrasjon"},
	"harRegistrertAntallAnsatte": true,
	"antallAnsatte": 487,
	"registrertIMvaregisteret": true,
	"overordnetEnhet": "912660680",
	"konkurs": false,
	"underAvvikling": false,
	"underTvangsavviklingEllerTvangsopplosning": false
}`

// validOrgNumbers are 30 organisation numbers that pass the register's own
// MOD-11 check digit (values.go's validNorwegianOrgNumber), written out as
// literals rather than computed here: a fixture derived from the validator
// under test would agree with it even when it is wrong.
var validOrgNumbers = []string{
	"923609016", "974760673", "912660680", "929745760", "981276957",
	"998989698", "919115505", "915635857", "995339668", "988925519",
	"976389387", "914994551", "983974724", "948007029", "961329310",
	"918983384", "980430022", "983887457", "984851006", "992919116",
	"917127972", "914797268", "915442552", "989757482", "996171205",
	"920434932", "910000004", "911234564", "912469131", "913703707",
}

// mustParseFeedDate is a fixture date as the instant the worker stored, for an
// assertion that compares the two.
func mustParseFeedDate(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.Parse("2006-01-02T15:04:05.000Z", value)
	if err != nil {
		t.Fatalf("parse the fixture date %q: %v", value, err)
	}
	return at.UTC()
}

// feedPageOf renders one feed page body for the given entries, in the shape
// the live API answers with (an entry's _links are omitted — this module never
// reads them, it builds the entity URL from the organisation number itself).
func feedPageOf(entries ...string) string {
	return `{"_embedded":{"oppdaterteEnheter":[` + strings.Join(entries, ",") + `]},` +
		`"page":{"size":1000,"totalElements":` + fmt.Sprint(len(entries)) + `,"totalPages":1,"number":0}}`
}

// feedEntryOf is one entry of that page.
func feedEntryOf(updateID int64, date, orgnr, changeType string) string {
	return fmt.Sprintf(`{"oppdateringsid":%d,"dato":%q,"organisasjonsnummer":%q,"endringstype":%q}`,
		updateID, date, orgnr, changeType)
}

// registryWorkerTransport answers both of the worker's reads: the feed on the
// oppdateringer path (each call taking the next body, the last one repeating),
// and the entity read on everything else. It records every request URI so a
// test can assert the cursor a page was asked for and that an unmatched entry
// cost no entity read at all.
type registryWorkerTransport struct {
	feed   *feedTransport
	entity func(path string) (*http.Response, error)
}

func newRegistryWorkerTransport(entity func(path string) (*http.Response, error), feedBodies ...string) *registryWorkerTransport {
	var n int
	w := &registryWorkerTransport{entity: entity}
	w.feed = &feedTransport{}
	w.feed.respond = func(uri string) (*http.Response, error) {
		if !strings.HasPrefix(uri, "/enhetsregisteret/api/oppdateringer/enheter") {
			return w.entity(uri)
		}
		i := n
		n++
		if i >= len(feedBodies) {
			i = len(feedBodies) - 1
		}
		return jsonResponse(http.StatusOK, feedBodies[i]), nil
	}
	return w
}

func (w *registryWorkerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return w.feed.RoundTrip(r)
}

func (w *registryWorkerTransport) requests() []string { return w.feed.requests() }

// entityAnswer is one canned entity response, as a status and a body rather
// than as a built *http.Response.
type entityAnswer struct {
	status int
	body   string
}

// entityBodies answers the entity read by organisation number, so one test can
// have one company change and another be removed.
//
// It takes statuses and bodies, and BUILDS A FRESH RESPONSE PER REQUEST, which
// is the whole reason this type exists: an *http.Response's Body is a reader,
// and the tests here deliberately read one company twice (a create hook stores
// the record, then a cycle re-reads it). A map of pre-built responses would hand
// the second read a body already drained to EOF — an empty body, decoded as an
// entity with no organisation number, which entity() then refuses for
// disagreeing with the request. registry_test.go's registryBody and
// registrySequence (`:114-143`) construct inside their closure for exactly this
// reason; so does this.
func entityBodies(byOrgNumber map[string]entityAnswer) func(string) (*http.Response, error) {
	return func(uri string) (*http.Response, error) {
		for orgnr, answer := range byOrgNumber {
			if strings.HasSuffix(uri, "/enhetsregisteret/api/enheter/"+orgnr) {
				return registryEntityResponse(answer.status, answer.body), nil
			}
		}
		return registryEntityResponse(http.StatusNotFound, ``), nil
	}
}

// feedFound and feedGone are entityAnswer's two spellings at a test's call
// site. The feed prefix is not decoration: this file's identifiers share one
// namespace with every other test in package customers_test, where "found" and
// "gone" on their own are words a dozen tests could want.
func feedFound(body string) entityAnswer { return entityAnswer{status: http.StatusOK, body: body} }
func feedGone(body string) entityAnswer  { return entityAnswer{status: http.StatusGone, body: body} }

// feedRequests is every feed request the worker made, in order — never "the
// last request": a cycle makes several and the cursor on each is the point.
func feedRequests(transport *registryWorkerTransport) []string {
	var out []string
	for _, uri := range transport.requests() {
		if strings.HasPrefix(uri, "/enhetsregisteret/api/oppdateringer/enheter") {
			out = append(out, uri)
		}
	}
	return out
}

// entityRequests is every entity read the worker made, in order.
func entityRequests(transport *registryWorkerTransport) []string {
	var out []string
	for _, uri := range transport.requests() {
		if strings.Contains(uri, "/enhetsregisteret/api/enheter/") {
			out = append(out, uri)
		}
	}
	return out
}

// cursorRow is the stored cursor's three moving parts.
func cursorRow(t *testing.T, h *modtest.Harness) (nextUpdateID *int64, lastUpdateAt *time.Time, lastPolledAt *time.Time) {
	t.Helper()
	row := h.Pool().QueryRow(context.Background(),
		`SELECT next_update_id, last_update_at, last_polled_at FROM customers.registry_feed_cursor WHERE id = 1`)
	if err := row.Scan(&nextUpdateID, &lastUpdateAt, &lastPolledAt); err != nil {
		t.Fatalf("read the cursor: %v", err)
	}
	return nextUpdateID, lastUpdateAt, lastPolledAt
}

// backfillPosition is where the sweep's backfill got to (design D3).
func backfillPosition(t *testing.T, h *modtest.Harness) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `SELECT backfill_after_id FROM customers.registry_feed_cursor WHERE id = 1`)
}

// registryHint is the stored hint for a customer, nil when there is none — and
// nil, not a failed test, when there is no record row at all: modtest.One
// fatals on zero rows, so the aggregate is what makes "no record yet" an answer
// this helper can return rather than a fixture error.
func registryHint(t *testing.T, h *modtest.Harness, customerID int32) *time.Time {
	t.Helper()
	return modtest.One[*time.Time](t, h,
		`SELECT max(registry_updated_hint) FROM customers.customer_registry_records WHERE customer_id = $1`, customerID)
}

// registryFetchedAt is the stored fetched_at for a customer. Unlike
// registryHint it fatals when there is no record: every caller has already
// established that there is one, and "no row" there would be the test's own
// setup being wrong.
func registryFetchedAt(t *testing.T, h *modtest.Harness, customerID int32) time.Time {
	t.Helper()
	return modtest.One[time.Time](t, h,
		`SELECT fetched_at FROM customers.customer_registry_records WHERE customer_id = $1`, customerID)
}

// TestValidOrgNumbersFixture guards the fixture above, not the code: a batch
// test that silently seeded customers the module considers unrefreshable would
// pass while asserting nothing, since registryOrganisationNumber skips them and
// the worker would make no request for any of them. Cheap, and it is what gives
// ValidNorwegianOrgNumberForTest its caller.
func TestValidOrgNumbersFixture(t *testing.T) {
	t.Parallel()
	if len(validOrgNumbers) != 30 {
		t.Fatalf("validOrgNumbers has %d entries, want 30 (the backfill batch of 25, plus five more to walk into)", len(validOrgNumbers))
	}
	seen := map[string]bool{}
	for _, n := range validOrgNumbers {
		if !customers.ValidNorwegianOrgNumberForTest(n) {
			t.Errorf("%s does not pass the check digit: replace it in the fixture", n)
		}
		if seen[n] {
			t.Errorf("%s appears twice: the batch test needs 30 distinct customers", n)
		}
		seen[n] = true
	}
}

// TestRegistryFeedWorker_BootstrapsByDateThenByCursor pins design D1's two
// request forms and the exact arithmetic between them: the first cycle has no
// cursor and joins the feed by date, and every cycle after it asks for the
// last id processed PLUS ONE, because oppdateringsid is inclusive. Getting
// that +1 wrong is invisible in production except as one entry re-read
// forever, which is why it is asserted on the URL and not only on the row.
func TestRegistryFeedWorker_BootstrapsByDateThenByCursor(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{}),
		feedPageOf(feedEntryOf(25255241, feedEntryDate, "929745760", "Endring")),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	w := customers.NewRegistryFeedWorker(h.Deps())

	ran, err := w.RunCycle(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
	first := feedRequests(transport)
	if len(first) == 0 || !strings.Contains(first[0], "dato=") {
		t.Fatalf("first feed request = %v, want a dato bootstrap", first)
	}
	next, lastUpdate, lastPolled := cursorRow(t, h)
	if next == nil || *next != 25255242 {
		t.Errorf("next_update_id = %v, want 25255242 (the last id plus one)", next)
	}
	if lastUpdate == nil || !lastUpdate.UTC().Equal(mustParseFeedDate(t, feedEntryDate)) {
		t.Errorf("last_update_at = %v, want the last entry's dato %s", lastUpdate, feedEntryDate)
	}
	if lastPolled == nil {
		t.Error("last_polled_at is NULL after a cycle that read the feed")
	}

	if ran, err := w.RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("second RunCycle = %v, %v; want true, nil", ran, err)
	}
	after := feedRequests(transport)
	if len(after) < 2 || !strings.Contains(after[1], "oppdateringsid=25255242") || strings.Contains(after[1], "dato=") {
		t.Errorf("second feed request = %v, want oppdateringsid=25255242 and no dato", after)
	}
}

// TestRegistryFeedWorker_RefreshesAMatchedCustomerAndIgnoresTheRest pins
// design D1's local intersection: an entry for a customer this installation
// has costs one entity read and one stored record, and an entry for a company
// nobody here is a customer of costs nothing at all. The unmatched half is the
// assertion that matters — a worker that read the entity for every entry in
// the feed would make the whole register's churn this installation's problem.
func TestRegistryFeedWorker_RefreshesAMatchedCustomerAndIgnoresTheRest(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		// The create hook stores the record first, from the body the register
		// held then; the cycle's own read is the one that differs, which is what
		// makes the event below evidence of a refresh rather than of the create.
		entityBodies(map[string]entityAnswer{
			"923609016": feedFound(equinorRegistryBody),
		}),
		feedPageOf(
			feedEntryOf(100, feedEntryDate, "929745760", "Endring"),
			feedEntryOf(101, feedEntryDateLater, "923609016", "Endring"),
		),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	if n := len(fetchRegistryEvents(t, c, created.Id)); n != 0 {
		t.Fatalf("registry.change events after the create = %d, want 0 (the pick's name matched)", n)
	}
	transport.entity = entityBodies(map[string]entityAnswer{
		"923609016": feedFound(movedEquinorRegistryBody),
	})
	h.Advance(afterTheFeed)

	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}

	// The create hook already read the entity once; the cycle's read is the
	// second, and it is for the matched number only.
	reads := entityRequests(transport)
	for _, uri := range reads {
		if strings.Contains(uri, "929745760") {
			t.Errorf("the worker read the entity for an unmatched organisation number: %v", reads)
		}
	}
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Fatalf("registry rows = %d, want 1", n)
	}
	// The hint the worker wrote before refreshing: the matched customer's own
	// entry, and nothing for the unmatched one (which has no record to hint at
	// in the first place).
	hint := registryHint(t, h, created.Id)
	if hint == nil || !hint.UTC().Equal(mustParseFeedDate(t, feedEntryDateLater)) {
		t.Errorf("hint = %v, want the matched entry's own date %s", hint, feedEntryDateLater)
	}
	// movedEquinorRegistryBody differs from what the create stored in two
	// fields, so the refresh actually happened and was diffed.
	events := fetchRegistryEvents(t, c, created.Id)
	if len(events) == 0 {
		t.Fatal("no registry.change event: the worker never refreshed the matched customer")
	}
	last := events[len(events)-1]
	if last.ActorKind != "system" || str(last.ActorDisplay) != "System" {
		t.Errorf("event actor = %q/%q, want system/System (design D4)", last.ActorKind, str(last.ActorDisplay))
	}
}

// TestRegistryFeedWorker_OneCustomerWithTwoEntriesIsRefreshedOnce pins design
// D1's per-customer deduplication and D2's newest-hint rule: a busy day can
// report the same company three times, and asking the registry three times for
// the same whole record would spend three requests to learn one thing.
func TestRegistryFeedWorker_OneCustomerWithTwoEntriesIsRefreshedOnce(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{
			"923609016": feedFound(movedEquinorRegistryBody),
		}),
		feedPageOf(
			feedEntryOf(200, feedEntryDate, "923609016", "Endring"),
			feedEntryOf(201, feedEntryDateLater, "923609016", "Endring"),
		),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	before := len(entityRequests(transport))
	h.Advance(afterTheFeed)

	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if got := len(entityRequests(transport)) - before; got != 1 {
		t.Errorf("entity reads during the cycle = %d, want exactly 1 for a customer named twice on one page", got)
	}
	// The hint written before the refresh is the NEWEST of the customer's two
	// entries — never the first one seen, and never absent.
	hint := registryHint(t, h, created.Id)
	if hint == nil {
		t.Fatal("no hint was written for a customer the page named twice")
	}
	if !hint.UTC().Equal(mustParseFeedDate(t, feedEntryDateLater)) {
		t.Errorf("hint = %v, want the newer of the two entries (%s)", hint.UTC(), feedEntryDateLater)
	}
	// And the successful refresh leaves fetched_at at or past it, which is what
	// makes the record NOT stale — the property the sweep's own select turns on.
	if fetched := registryFetchedAt(t, h, created.Id); fetched.Before(*hint) {
		t.Errorf("fetched_at %v is before the hint %v after a successful refresh", fetched, *hint)
	}
}

// TestRegistryFeedWorker_AnOlderEntryOnALaterPageDoesNotLowerTheHint pins
// SetRegistryUpdatedHint's never-backwards clause across two pages (final fix
// wave M7). A backlog caught up in one cycle, or a page the registry serves
// unsorted, can name the same company twice with the older report arriving
// second; a hint that moved backwards would make a record look FRESHER than the
// register said it was, which is the one direction that loses a change — the
// sweep's "hint > fetched_at" would stop being true and nothing would ever
// re-read the entity.
//
// The page size is 1 through the test seam, so each entry is a full page and the
// loop reads both.
func TestRegistryFeedWorker_AnOlderEntryOnALaterPageDoesNotLowerTheHint(t *testing.T) {
	restore := customers.SetRegistryFeedPageSize(1)
	defer restore()

	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{
			"923609016": feedFound(equinorRegistryBody),
		}),
		// The newer report first, then the older one.
		feedPageOf(feedEntryOf(1100, feedEntryDateLater, "923609016", "Endring")),
		feedPageOf(feedEntryOf(1101, feedEntryDate, "923609016", "Endring")),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	h.Advance(afterTheFeed)

	if _, err := customers.NewRegistryFeedWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	hint := registryHint(t, h, created.Id)
	if hint == nil {
		t.Fatal("no hint was written at all")
	}
	if !hint.UTC().Equal(mustParseFeedDate(t, feedEntryDateLater)) {
		t.Errorf("hint = %v, want the newer of the two reports (%s): a hint never moves backwards",
			hint.UTC(), feedEntryDateLater)
	}
}

// TestRegistryFeedWorker_AnArchivedCustomerIsNotMatchedOnAPage pins
// CustomersByOrganisationNumbers's archived filter (final fix wave M7). An
// archived customer is invoiced by nobody and shown to nobody, so a page naming
// its company must cost no entity read at all — on an installation with years of
// archived customers that filter is the difference between one request per
// changed company and one per changed company that was ever a customer.
func TestRegistryFeedWorker_AnArchivedCustomerIsNotMatchedOnAPage(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{
			"923609016": feedFound(equinorRegistryBody),
		}),
		feedPageOf(feedEntryOf(1200, feedEntryDate, "923609016", "Endring")),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	h.Exec(t, `UPDATE customers.customers SET status = 'archived' WHERE id = $1`, created.Id)
	before := len(entityRequests(transport))
	h.Advance(afterTheFeed)

	if _, err := customers.NewRegistryFeedWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if got := len(entityRequests(transport)) - before; got != 0 {
		t.Errorf("entity reads during the cycle = %d, want 0: the one customer with this number is archived", got)
	}
	if hint := registryHint(t, h, created.Id); hint != nil {
		t.Errorf("hint = %v, want none: an archived customer is not on the page's match list", hint)
	}
	// The cursor still moves: the page was accounted for — every entry on it was
	// considered, and none of them was this installation's.
	if next, _, _ := cursorRow(t, h); next == nil || *next != 1201 {
		t.Errorf("next_update_id = %v, want 1201: a page that matched nobody is still a processed page", next)
	}
}

// TestRegistryFeedWorker_AFailedFeedRequestLeavesTheCursorAlone pins design
// D1's failure rule. A cursor advanced past a page that was never read is a
// silent, permanent hole in the record: those entities changed, nothing here
// knows, and nothing ever will.
func TestRegistryFeedWorker_AFailedFeedRequestLeavesTheCursorAlone(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{}),
		feedPageOf(feedEntryOf(300, feedEntryDate, "929745760", "Endring")),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("first RunCycle: %v", err)
	}
	next, _, _ := cursorRow(t, h)
	if next == nil {
		t.Fatal("the first cycle did not advance the cursor")
	}
	at := *next

	// From here on the feed is down.
	transport.feed.respond = func(uri string) (*http.Response, error) {
		if strings.HasPrefix(uri, "/enhetsregisteret/api/oppdateringer/enheter") {
			return jsonResponse(http.StatusInternalServerError, `{}`), nil
		}
		return registryEntityResponse(http.StatusNotFound, ``), nil
	}
	if _, err := w.RunCycle(context.Background()); err == nil {
		t.Error("RunCycle reported success while the feed was failing")
	}
	after, _, _ := cursorRow(t, h)
	if after == nil || *after != at {
		t.Errorf("next_update_id = %v after a failed request, want it unchanged at %d", after, at)
	}
}

// TestRegistryFeedWorker_AShortPageEndsTheCycle pins the stopping rule: fewer
// entries than asked for means the feed is caught up, and one more request
// would only ask the registry to say so again.
func TestRegistryFeedWorker_AShortPageEndsTheCycle(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{}),
		feedPageOf(feedEntryOf(400, feedEntryDate, "929745760", "Endring")),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if got := feedRequests(transport); len(got) != 1 {
		t.Errorf("feed requests = %d, want 1: a short page ends the cycle", len(got))
	}
}

// TestRegistryFeedWorker_ThePageBudgetEndsTheCycleToo pins the other bound: a
// backlog is caught up over several cycles rather than in one unbounded run,
// so a worker that falls a month behind never holds its lease for an hour.
// The page size is dropped to 1 through the test seam, so one entry is a FULL
// page and the loop keeps asking until the budget stops it.
func TestRegistryFeedWorker_ThePageBudgetEndsTheCycleToo(t *testing.T) {
	restore := customers.SetRegistryFeedPageSize(1)
	defer restore()

	var id int64 = 500
	transport := newRegistryWorkerTransport(entityBodies(map[string]entityAnswer{}))
	transport.feed.respond = func(uri string) (*http.Response, error) {
		if !strings.HasPrefix(uri, "/enhetsregisteret/api/oppdateringer/enheter") {
			return registryEntityResponse(http.StatusNotFound, ``), nil
		}
		id++
		return jsonResponse(http.StatusOK, feedPageOf(
			feedEntryOf(id, feedEntryDate, "929745760", "Endring"))), nil
	}
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if got := len(feedRequests(transport)); got != 20 {
		t.Errorf("feed requests = %d, want the 20-page budget", got)
	}
}

// TestRegistryFeedWorker_FjernetDeletesTheRecordWithOneEvent pins design D4's
// outcome mapping through the worker: the feed's "Fjernet" is not itself the
// authority — the entity read is, answering 410 — and the worker takes exactly
// the same path a click does, deleting the copy and leaving one event behind.
func TestRegistryFeedWorker_FjernetDeletesTheRecordWithOneEvent(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		// The create hook reads the entity first and stores the record, so there
		// is a copy for the cycle to remove; the register loses the entity
		// between the two reads.
		entityBodies(map[string]entityAnswer{
			"923609016": feedFound(equinorRegistryBody),
		}),
		feedPageOf(feedEntryOf(600, feedEntryDate, "923609016", "Fjernet")),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Fatalf("registry rows after create = %d, want 1", n)
	}
	transport.entity = entityBodies(map[string]entityAnswer{
		"923609016": feedGone(removedRegistryBody),
	})
	h.Advance(afterTheFeed)

	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0: a 410 deletes the copy", n)
	}
	// recordRegistryRemoved's summary is a fixed literal, not built from the
	// diff (timeline_events.go): "removedFromOpenData" is the payload's field
	// name and never appears in the sentence.
	events := fetchRegistryEvents(t, c, created.Id)
	var removed int
	for _, e := range events {
		if str(e.Summary) == "Registry record removed from open data" {
			removed++
		}
	}
	if removed != 1 {
		t.Errorf("removal events = %d, want exactly 1; summaries = %v", removed, events)
	}
}

// TestRegistryFeedWorker_AFailedRefreshLeavesTheHintForTheSweep pins the
// handshake between D2 and D3: the hint is written before the refresh, so a
// refresh that fails leaves hint > fetched_at, and the NEXT cycle's sweep —
// not the feed, which has moved on — is what retries it. Without the ordering
// this test would pass by accident with no retry at all.
func TestRegistryFeedWorker_AFailedRefreshLeavesTheHintForTheSweep(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{
			"923609016": feedFound(equinorRegistryBody),
		}),
		feedPageOf(feedEntryOf(700, feedEntryDate, "923609016", "Endring")),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

	// The feed still answers; the entity read does not.
	transport.entity = func(string) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, `{}`), nil
	}
	h.Advance(afterTheFeed)
	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	hint, fetched := registryHint(t, h, created.Id), registryFetchedAt(t, h, created.Id)
	if hint == nil {
		t.Fatal("no hint was written: a failed refresh must still leave the feed's report on the row")
	}
	if !hint.After(fetched) {
		t.Fatalf("hint = %v, fetched_at = %v; want hint > fetched_at, the definition of stale", hint.UTC(), fetched)
	}

	// The registry comes back, and the next cycle's sweep — which reads no feed
	// entry for this customer at all (the second page is empty) — refreshes it.
	transport.entity = entityBodies(map[string]entityAnswer{
		"923609016": feedFound(movedEquinorRegistryBody),
	})
	h.Advance(time.Minute)
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("second RunCycle: %v", err)
	}
	if fetched := registryFetchedAt(t, h, created.Id); fetched.Before(*hint) {
		t.Errorf("still stale after the sweep: fetched_at %v, hint %v", fetched, hint.UTC())
	}
}

// TestRegistryFeedWorker_TheBackfillPicksUpACustomerWithNoRecord pins design
// D3's second half: a customer that predates delivery A, or whose first fetch
// failed, gets its record without anyone clicking — and the ordinary
// first-fetch diff still applies, so a hand-typed name that differs from the
// registry's is reported exactly as a click would report it.
func TestRegistryFeedWorker_TheBackfillPicksUpACustomerWithNoRecord(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{
			"923609016": feedFound(equinorRegistryBody),
		}),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	// createCustomerWithIdentity's identity is source "manual" already, and no
	// create hook fetches for a manual pick (design D2) — so this customer has
	// no record at all until the backfill finds it.
	created := createCustomerWithIdentity(t, c, "Equinor, typed by hand", "no", "923609016")
	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Fatalf("registry rows before the sweep = %d, want 0", n)
	}

	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Errorf("registry rows after the sweep = %d, want 1", n)
	}
	// The typed name is not the registry's, so the first-fetch diff says so.
	events := fetchRegistryEvents(t, c, created.Id)
	if len(events) != 1 || !strings.Contains(str(events[0].Summary), "name") {
		t.Errorf("events = %+v, want one naming the name difference", events)
	}
}

// TestRegistryFeedWorker_ARefreshWhoseCustomerWasReIdentifiedWritesNothing pins
// the identity re-check inside refreshRegistryRecord's own transaction (final
// fix wave I4). The organisation number is resolved BEFORE the network call —
// it has to be, the call is made for it — and a person re-identifying the
// customer in that window would otherwise have another company's record written
// under its id: invisible (the GET's own guard hides a record whose number is
// not the customer's), unrepairable (both sweeps skip it for the same reason)
// and permanent (the backfill sees a row, so it never fetches the right one).
//
// The transport is the seam, because the window is exactly "the entity read":
// the identity changes while the register is answering, which is the race as it
// actually happens.
func TestRegistryFeedWorker_ARefreshWhoseCustomerWasReIdentifiedWritesNothing(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(nil, feedPageOf())
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	// A manual identity, so no create hook fetches anything: this customer has no
	// record at all until the sweep's backfill goes looking for it.
	created := createCustomerWithIdentity(t, c, "Equinor, typed by hand", "no", "923609016")

	var moved bool
	transport.entity = func(uri string) (*http.Response, error) {
		if !moved && strings.HasSuffix(uri, "/923609016") {
			// The register is answering about 923609016; by the time the answer is
			// stored, this customer is a different company.
			moved = true
			h.Exec(t, `UPDATE customers.customers SET legal_id = '974760673' WHERE id = $1`, created.Id)
		}
		return registryEntityResponse(http.StatusOK, equinorRegistryBody), nil
	}

	if ran, err := customers.NewRegistryFeedWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil — a re-identified customer is not a failed cycle", ran, err)
	}
	if !moved {
		t.Fatal("the entity was never read: this test never reached the race it is about")
	}
	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0: the answer was about the company this customer no longer is", n)
	}
	if events := fetchRegistryEvents(t, c, created.Id); len(events) != 0 {
		t.Errorf("registry events = %+v, want none: nothing was written, so nothing is worth reporting", events)
	}
}

// TestRegistryFeedWorker_TheBackfillWalksPastItsBatchAndStartsOver pins both
// halves of design D3's backfill: the batch BOUND (a few dozen a cycle, not
// every customer at once the first time the worker ever runs) and the fact that
// it walks.
//
// The walking is the part a reasonable implementer leaves out, and leaving it
// out is a silent, permanent starvation: these 30 customers all answer 404, as
// a customer whose typed organisation number the register does not know always
// will, so nothing ever gets a record and "the 25 lowest ids with no record" is
// the same 25 rows on every cycle for the rest of the installation's life.
// Customer 26 is then never read, ever — and nothing fails, which is why it has
// its own test rather than a line in another one.
func TestRegistryFeedWorker_TheBackfillWalksPastItsBatchAndStartsOver(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		func(string) (*http.Response, error) {
			return registryEntityResponse(http.StatusNotFound, ``), nil
		},
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	// 30 Norwegian business customers with valid organisation numbers and no
	// record. The batch is 25, so the first cycle takes 25, the second takes the
	// remaining 5 and — being short — resets, and the third starts over.
	ids := make([]int32, 0, len(validOrgNumbers))
	for i, orgnr := range validOrgNumbers {
		name := fmt.Sprintf("Backfill %d", i)
		id := insertCustomer(t, h, name, "active")
		h.Exec(t, `UPDATE customers.customers
			SET legal_country = 'no', legal_type = 'business', legal_source = 'manual',
			    legal_id = $2, legal_name = $3
			WHERE id = $1`, id, orgnr, name)
		ids = append(ids, id)
	}

	// A 404 stores nothing, so the entity reads are the only evidence of what
	// the sweep attempted — and which numbers they name is the evidence of
	// where it attempted it.
	reads := func() []string { return entityRequests(transport) }
	w := customers.NewRegistryFeedWorker(h.Deps())

	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("first RunCycle: %v", err)
	}
	if got := len(reads()); got != 25 {
		t.Fatalf("entity reads after cycle 1 = %d, want the 25-customer backfill batch", got)
	}
	if got := backfillPosition(t, h); got != ids[24] {
		t.Errorf("backfill_after_id = %d after a full batch, want the 25th customer's id %d", got, ids[24])
	}

	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("second RunCycle: %v", err)
	}
	second := reads()[25:]
	if len(second) != 5 {
		t.Fatalf("entity reads during cycle 2 = %d, want the remaining 5", len(second))
	}
	// The five it had not reached yet, not the same first 25 again.
	for i, uri := range second {
		if !strings.HasSuffix(uri, "/"+validOrgNumbers[25+i]) {
			t.Errorf("cycle 2's read %d = %s, want the customer after the 25th (%s)", i, uri, validOrgNumbers[25+i])
		}
	}
	if got := backfillPosition(t, h); got != 0 {
		t.Errorf("backfill_after_id = %d after a short batch, want 0 so the next pass starts over", got)
	}

	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("third RunCycle: %v", err)
	}
	third := reads()[30:]
	if len(third) != 25 || !strings.HasSuffix(third[0], "/"+validOrgNumbers[0]) {
		t.Errorf("cycle 3 read %d entities starting at %v, want 25 starting over at the first customer", len(third), third[:1])
	}
}

// TestRegistryFeedWorker_TheBackfillStartsOverAfterAnEmptyBatch is the boundary
// the test above steps over: EXACTLY one batch of candidates, all unresolvable.
// The first cycle takes all 25 and — the batch being full — leaves the position
// at the 25th, because there might be a 26th customer. There is not, so the
// second cycle's batch is empty; if an empty batch left the position alone, it
// would stay at the 25th forever and those 25 customers would never be attempted
// again. The reset is what makes the third cycle try them.
func TestRegistryFeedWorker_TheBackfillStartsOverAfterAnEmptyBatch(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		func(string) (*http.Response, error) {
			return registryEntityResponse(http.StatusNotFound, ``), nil
		},
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	var last int32
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("Exactly %d", i)
		last = insertCustomer(t, h, name, "active")
		h.Exec(t, `UPDATE customers.customers
			SET legal_country = 'no', legal_type = 'business', legal_source = 'manual',
			    legal_id = $2, legal_name = $3
			WHERE id = $1`, last, validOrgNumbers[i], name)
	}

	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("first RunCycle: %v", err)
	}
	if got, n := backfillPosition(t, h), len(entityRequests(transport)); got != last || n != 25 {
		t.Fatalf("after cycle 1: position %d, reads %d; want %d and 25", got, n, last)
	}

	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("second RunCycle: %v", err)
	}
	if n := len(entityRequests(transport)); n != 25 {
		t.Errorf("reads after cycle 2 = %d, want still 25: there was nothing after the position", n)
	}
	if got := backfillPosition(t, h); got != 0 {
		t.Fatalf("position = %d after an empty batch, want 0 — otherwise the backfill is parked there for good", got)
	}

	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("third RunCycle: %v", err)
	}
	if n := len(entityRequests(transport)); n != 50 {
		t.Errorf("reads after cycle 3 = %d, want 50: the reset made the same 25 candidates eligible again", n)
	}
}

// TestRegistryFeedWorker_ACancelledBackfillLeavesItsPositionAlone pins what a
// cycle that is shut down mid-batch claims: nothing. The position is the
// promise "everything up to here has been attempted this pass", and a cycle
// cancelled after its first customer has not attempted the other 24 — writing
// the position anyway would send them to the back of the queue behind a whole
// pass of other customers, for no reason but the moment the process happened to
// stop. Not writing it costs the one row that was attempted a second attempt
// next cycle, which is a request, not a hole.
//
// The cancellation lands inside the first entity read, which is the only place
// in a sweep where a shutdown realistically catches it.
func TestRegistryFeedWorker_ACancelledBackfillLeavesItsPositionAlone(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := newRegistryWorkerTransport(
		func(string) (*http.Response, error) {
			// The shutdown arrives while the register is answering about the first
			// customer. 404 either way: this test is about the position, not about
			// what the register said.
			cancel()
			return registryEntityResponse(http.StatusNotFound, ``), nil
		},
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	ids := make([]int32, 0, 5)
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("Cancelled %d", i)
		id := insertCustomer(t, h, name, "active")
		h.Exec(t, `UPDATE customers.customers
			SET legal_country = 'no', legal_type = 'business', legal_source = 'manual',
			    legal_id = $2, legal_name = $3
			WHERE id = $1`, id, validOrgNumbers[i], name)
		ids = append(ids, id)
	}
	// A pass already under way, one customer in: the candidates are the four
	// after ids[0], so a position that moved at all is visible as a change.
	h.Exec(t, `INSERT INTO customers.registry_feed_cursor (id, started_at, backfill_after_id) VALUES (1, $1, $2)`,
		h.Now(), ids[0])

	if _, err := customers.NewRegistryFeedWorker(h.Deps()).RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if n := len(entityRequests(transport)); n != 1 {
		t.Fatalf("entity reads = %d, want 1: the cycle stopped after the first customer", n)
	}
	if got := backfillPosition(t, h); got != ids[0] {
		t.Errorf("backfill_after_id = %d after a cancelled batch, want it unchanged at %d — the four customers this cycle never attempted are next cycle's, not next pass's", got, ids[0])
	}
}

// TestRegistryFeedWorker_ABlackHoledRegistryEndsTheCycleEarly pins the outage
// bound (final fix wave I5). The arithmetic is the reason: a sweep is up to 75
// refreshes, each of which spends the whole BRREG_TIMEOUT budget against a
// registry that answers nothing — nineteen minutes of one cycle at the default
// fifteen-second timeout, still holding the lease, after which the 15-minute
// ticker fires and the next cycle does it again. So a cycle that has met the
// registry's silence five times in a row stands down until the next poll, having
// claimed no ground: the backfill position is where it was, the feed was never
// asked, and everything is retried next cycle.
func TestRegistryFeedWorker_ABlackHoledRegistryEndsTheCycleEarly(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		// Every entity read is a 500, which is exactly what a black hole behind a
		// load balancer looks like: retryable, so each refresh also spends its whole
		// retry budget on it.
		func(string) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, `{}`), nil
		},
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	// Ten backfill candidates, so a cycle that did not stand down would attempt
	// all ten.
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("Unreachable %d", i)
		id := insertCustomer(t, h, name, "active")
		h.Exec(t, `UPDATE customers.customers
			SET legal_country = 'no', legal_type = 'business', legal_source = 'manual',
			    legal_id = $2, legal_name = $3
			WHERE id = $1`, id, validOrgNumbers[i], name)
	}

	ran, err := customers.NewRegistryFeedWorker(h.Deps()).RunCycle(context.Background())
	if err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil — an unreachable registry is not a failed cycle", ran, err)
	}
	// Each refresh spends its retry budget, so count the CUSTOMERS attempted, not
	// the requests: the bound is on how many companies a dead registry costs.
	attempted := map[string]bool{}
	for _, uri := range entityRequests(transport) {
		attempted[uri[strings.LastIndex(uri, "/")+1:]] = true
	}
	if len(attempted) != 5 {
		t.Errorf("customers attempted = %d (%v), want the 5-refresh outage limit", len(attempted), attempted)
	}
	if got := backfillPosition(t, h); got != 0 {
		t.Errorf("backfill_after_id = %d, want it untouched at 0: an abandoned batch claims no ground", got)
	}
	if got := feedRequests(transport); len(got) != 0 {
		t.Errorf("feed requests = %v, want none: the feed is the same registry the sweep just gave up on", got)
	}
	if next, _, _ := cursorRow(t, h); next != nil {
		t.Errorf("next_update_id = %v, want NULL: no page was read, so there is no position to claim", *next)
	}
}

// TestRegistryFeedWorker_AdvancesPastAPageOneRefreshCouldNotFinish pins the
// division of labour between the cursor and the hint (design D1, D2): the
// cursor records which feed ENTRIES have been accounted for, not which
// refreshes succeeded. A cursor held back until every refresh on the page
// worked would re-read that page — and re-refresh everyone else on it — on
// every cycle for as long as one company stayed unreachable. The hint is what
// remembers the one that failed, and the sweep is what retries it.
func TestRegistryFeedWorker_AdvancesPastAPageOneRefreshCouldNotFinish(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{
			"923609016": feedFound(equinorRegistryBody),
			"974760673": feedFound(brregEntityRegistryBody),
		}),
		feedPageOf(
			feedEntryOf(900, feedEntryDate, "923609016", "Endring"),
			feedEntryOf(901, feedEntryDate, "974760673", "Endring"),
		),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	first := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	second := createBrregPick(t, c, "REGISTERENHETEN I BRØNNØYSUND", "974760673")

	// The second customer's entity read now fails; the first's still works.
	transport.entity = func(uri string) (*http.Response, error) {
		if strings.HasSuffix(uri, "/974760673") {
			return jsonResponse(http.StatusInternalServerError, `{}`), nil
		}
		return registryEntityResponse(http.StatusOK, movedEquinorRegistryBody), nil
	}
	h.Advance(afterTheFeed)

	if _, err := customers.NewRegistryFeedWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	next, _, _ := cursorRow(t, h)
	if next == nil || *next != 902 {
		t.Errorf("next_update_id = %v, want 902: the page was accounted for even though one refresh failed", next)
	}
	// The one that worked is current; the one that failed is stale, and stale is
	// what the next cycle's sweep reads.
	if fetched, hint := registryFetchedAt(t, h, first.Id), registryHint(t, h, first.Id); hint == nil || fetched.Before(*hint) {
		t.Errorf("the successful customer is stale: fetched_at %v, hint %v", fetched, hint)
	}
	if fetched, hint := registryFetchedAt(t, h, second.Id), registryHint(t, h, second.Id); hint == nil || !hint.After(fetched) {
		t.Errorf("the failed customer is not stale: fetched_at %v, hint %v", fetched, hint)
	}
}

// TestRegistryFeedWorker_AFailedSecondPageKeepsTheFirstPagesCursor pins that
// the cursor is written PER PAGE, not per cycle: a cycle that reads three pages
// and fails on the fourth must keep the three, or an installation whose feed is
// flaky enough to fail mid-cycle never advances at all — it re-reads the same
// pages every fifteen minutes forever.
//
// The page size is 1 through the test seam, so the first entry is a full page
// and the loop asks for a second one, which fails.
func TestRegistryFeedWorker_AFailedSecondPageKeepsTheFirstPagesCursor(t *testing.T) {
	restore := customers.SetRegistryFeedPageSize(1)
	defer restore()

	var asked int
	transport := newRegistryWorkerTransport(entityBodies(map[string]entityAnswer{}))
	transport.feed.respond = func(uri string) (*http.Response, error) {
		if !strings.HasPrefix(uri, "/enhetsregisteret/api/oppdateringer/enheter") {
			return registryEntityResponse(http.StatusNotFound, ``), nil
		}
		asked++
		if asked == 1 {
			return jsonResponse(http.StatusOK, feedPageOf(
				feedEntryOf(1000, feedEntryDate, "929745760", "Endring"))), nil
		}
		return jsonResponse(http.StatusInternalServerError, `{}`), nil
	}
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))

	if _, err := customers.NewRegistryFeedWorker(h.Deps()).RunCycle(context.Background()); err == nil {
		t.Fatal("RunCycle reported success although the second page's request failed")
	}
	next, lastUpdate, _ := cursorRow(t, h)
	if next == nil || *next != 1001 {
		t.Errorf("next_update_id = %v, want 1001: the first page was processed and is not read again", next)
	}
	if lastUpdate == nil || !lastUpdate.UTC().Equal(mustParseFeedDate(t, feedEntryDate)) {
		t.Errorf("last_update_at = %v, want the first page's own entry date", lastUpdate)
	}
}

// TestRegistryFeedWorker_SkipsTheCycleWhenTheLeaseIsHeld is design D5 through
// this worker: a second replica logs and skips rather than reading the same
// pages and advancing the same cursor. Modelled on communications'
// TestRetentionWorker_SkipsTheCycleWhenTheLeaseIsHeld.
func TestRegistryFeedWorker_SkipsTheCycleWhenTheLeaseIsHeld(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(entityBodies(map[string]entityAnswer{}), feedPageOf())
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))

	ctx := context.Background()
	holder, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the lease holder's connection: %v", err)
	}
	defer holder.Release()
	var locked bool
	if err := holder.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, customers.RegistryFeedLeaseKeyForTest).Scan(&locked); err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	if !locked {
		t.Fatal("the lease was already held; this test's database is its own")
	}

	w := customers.NewRegistryFeedWorker(h.Deps())
	ran, err := w.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if ran {
		t.Error("RunCycle ran while another holder had the lease")
	}
	if len(feedRequests(transport)) != 0 {
		t.Error("the worker read the feed despite the lease being held elsewhere")
	}

	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, customers.RegistryFeedLeaseKeyForTest); err != nil {
		t.Fatalf("release the lease: %v", err)
	}
	ran, err = w.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle after release: %v", err)
	}
	if !ran {
		t.Fatal("RunCycle still reported the lease held after it was released")
	}
}

// TestRegistryFeedWorker_ReleasesTheLeaseAfterEveryCycle pins the deferred
// release: the lock is session-scoped, so a cycle that forgot to unlock would
// wedge this replica's pooled connection and every later cycle on it would
// silently skip.
func TestRegistryFeedWorker_ReleasesTheLeaseAfterEveryCycle(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(entityBodies(map[string]entityAnswer{}), feedPageOf())
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	w := customers.NewRegistryFeedWorker(h.Deps())
	for i := 0; i < 3; i++ {
		ran, err := w.RunCycle(context.Background())
		if err != nil {
			t.Fatalf("RunCycle %d: %v", i, err)
		}
		if !ran {
			t.Fatalf("RunCycle %d reported the lease held: the previous cycle did not release it", i)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); n != 0 {
		t.Errorf("advisory locks still held on this database = %d, want 0", n)
	}
}

// TestRegistryFeedWorker_RunStopsWithItsContext pins the loop contract the
// runner depends on: Run returns nil once ctx is done rather than running
// forever (internal/worker's Worker doc).
func TestRegistryFeedWorker_RunStopsWithItsContext(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]entityAnswer{
			"923609016": feedFound(movedEquinorRegistryBody),
		}),
		feedPageOf(feedEntryOf(800, feedEntryDate, "923609016", "Endring")),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	h.Advance(afterTheFeed)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- customers.NewRegistryFeedWorker(h.Deps()).Run(ctx) }()

	// The first cycle runs immediately, before the first tick.
	deadline := time.Now().Add(10 * time.Second)
	for registryHint(t, h, created.Id) == nil {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("Run never processed the first page")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil on a cancelled context", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
