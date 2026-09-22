# Registry Workers (phase 3, delivery B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the registry record and the Peppol answer current without anyone clicking: one worker reads Brønnøysundregistrene's incremental update feed from a stored cursor and re-reads every matched customer through delivery A's own refresh path (sweeping retries and never-fetched customers first), a second worker re-asks the Peppol network on a schedule, and the Registry card gains one line when the feed knows something the record does not.

**Architecture:** The Brreg client gains a second read — the update feed — beside `entity`. A one-row cursor table (`customers.registry_feed_cursor`, migration `00023`) holds the exact `oppdateringsid` to resume from. `RegistryFeedWorker` takes a session-scoped advisory lease, sweeps (stale records, then customers with no record at all), then reads feed pages, intersects each page with this installation's non-archived Norwegian business customers locally, writes `registry_updated_hint` and calls delivery A's `refreshRegistryRecord` with the system actor. `PeppolRecheckWorker` does the same under its own lease over the handler's own lookup-and-store, extracted so click and worker share one function. The contract gains one optional field; nothing else a user sees changes.

**Tech Stack:** Go 1.27 (pgx, sqlc, goose, oapi-codegen, `internal/worker`), PostgreSQL 18, React + Mantine + TanStack Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-22-customers-registry-workers-design.md` (D1–D7; "What the feed looks like" was verified against the live API on 2026-09-22 — use its parameter names, field names and `endringstype` values verbatim).

## Global Constraints

- Branch `feat/customers-registry-workers` (already checked out). Never commit to `main`, never merge, never `--no-verify`.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`. Commit with an explicit pathspec (`git add <files>` then `git commit -F <msgfile> -- <files>`), then check `git show --stat HEAD` and that `git status --short` shows nothing of yours left. The untracked `go.mod`/`go.sum` in the repo root are not ours: never add or delete them.
- Commit messages: Conventional Commits scoped `customers` / `customers-ui` / `contracts` / `docs`, subject a plain sentence about behaviour (see `git log`). End every message with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` exactly.
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- bun …`. Capture exit codes before any pipe (`${PIPESTATUS[0]}`).
- Go tests need `TEST_DATABASE_URL` exported in the environment before the first `go test`. **Take the value from the environment** (the project's own test database); this plan does not name one. If it is unset, stop and ask — do not guess a port.
- **No test may touch the real network.** `Deps.HTTPTransport` is the seam for every Brreg request (feed and entity alike) and `Deps.PeppolLookup` for every Peppol lookup; `modtest.WithTransport` / `modtest.WithPeppolLookup` install the fakes. No test in this delivery opens a socket or resolves a name.
- **The frozen corpus** `openapi/testdata/exchanges/customers.jsonl` is never edited, for any reason.
- **Contract changes are additive and optional only**: a new property on an existing schema is optional (never added to an existing `required:` list). Removing `countryCode` from `CustomerRegistryAddress.required` is the one relaxation this delivery makes, and it is a relaxation — no client can break on a field it still receives whenever the registry has one.
- **Every worker refresh is attributed to `generatedFallbackActor`** (`actor{Kind: "system", Display: "System"}`, `actor.go:38-40`). A worker never calls `s.actorFor`: there is no principal in a worker's context, and the fallback is D1's own answer for "no user to attribute to".
- **The network call never happens inside a transaction.** `refreshRegistryRecord` and `lookupAndStorePeppol` both fetch first and open their transaction afterwards; nothing in this delivery may reverse that.
- After any `openapi/*.yaml` or `queries/*.sql` or migration change: `cd apps/server && mise exec -- go generate ./...` (a second run must show no new diff), then from the repo root `mise exec -- bun run gen:client` for a yaml change. Commit every generated file (`apps/server/internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `internal/customers/store/*.go`, each changed `api-schema.d.ts`, `openapi/COVERAGE.md` if it moved).
- After any migration: add it to `apps/server/internal/customers/sqlc.yaml`'s `schema:` list (`TestSqlcSchemaListsOnlyTheModulesOwnMigrations` enforces the list is exactly this module's migrations) and to `internal/db/schema_test.go`'s `wantTables`, then `go generate`.
- After any exported Go signature change: `mise exec -- go vet ./...` and grep every caller including tests and fakes.
- Foundation rules that still bind: nothing here writes `customers.customers`, so nothing bumps `revision`; the record's own table is the only thing a refresh writes; `UpsertCustomerRegistryRecord` still never writes `registry_updated_hint` (only this delivery's own statement does).
- Match the surrounding code: comment density and voice (these files explain *why*), naming, error wording, test style. Worker shape — `Run`/`RunCycle`/`underLease`/`now()`/`logger()` — is copied from `internal/communications/retention.go`, reproduced rather than shared (spec D5).
- Every new UI string in both catalogs of `apps/customers/frontend/src/i18n.ts` (en + nb); `mise exec -- bun run translations:check` and `mise exec -- bun run i18n:test` must pass.
- Frontend tests: `mise exec -- bun run --cwd apps/customers/frontend test`. Mantine needs `<MantineProvider env="test">`. **Never assert "the last fetch"** — filter `fetchMock.mock.calls` by method and URL.
- Every new test must be shown able to fail (remove the guard, see red, restore). Say so in the report.
- **One implementer commits at a time.** If two agents share the tree, the second writes and verifies but does not commit.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/customers/brreg_feed.go` | the feed request: `feedCursor` → one `feedPage`, retries, caps, error kinds |
| `apps/server/internal/db/migrations/00023_customers_registry_feed.sql` | the one-row cursor table |
| `apps/server/internal/customers/queries/registry_feed.sql` | cursor read/write, the hint write, the two sweep selects, the page's customer match |
| `apps/server/internal/customers/registry_feed_worker.go` | `RegistryFeedWorker`: lease, cycle, sweep, page loop, cursor advance |
| `apps/server/internal/customers/queries/peppol_recheck.sql` | the two Peppol candidate selects |
| `apps/server/internal/customers/peppol_lookup.go` | gains `lookupAndStorePeppol`, the click and the worker's one shared function |
| `apps/server/internal/customers/peppol_recheck_worker.go` | `PeppolRecheckWorker`: lease, cycle, the two candidate sets |
| `apps/server/internal/customers/module.go` | `Workers` field: which workers this installation's configuration starts |
| `apps/server/internal/config/config.go` | the five new variables |
| `apps/server/internal/customers/registry.go` | the hint on the stored record and on the wire |
| `apps/customers/frontend/src/api/registry.ts`, `src/pages/-customer-registry-card.tsx`, `src/i18n.ts` | the hint line, the relaxed `countryCode` |

---

### Task 1: The Brreg client reads the update feed (D1)

**Files:**
- Create: `apps/server/internal/customers/brreg_feed.go`, `apps/server/internal/customers/brreg_feed_test.go`
- Read first (do not change): `apps/server/internal/customers/brreg_entity.go` (the shape this file copies), `brreg.go:131-212` (`brregRetryAttempts`, `brregAttemptTimeout`, `waitBackoff`, `isSuccessStatus`, `isRetryableStatus`, `errBrregUnavailable`), `registry_test.go:88-143` (`registryTransport`, `registryEntityResponse`)

**Interfaces:**
- Consumes: `*brregClient` (`brreg.go:219-245`, built by `newServer`), `c.timeout`, `c.backoff`, `c.client`.
- Produces:
```go
const (
	brregFeedPath         = "/enhetsregisteret/api/oppdateringer/enheter"
	brregFeedMaxBodyBytes = 4 << 20
	brregFeedDateLayout   = "2006-01-02T15:04:05.000Z"
)

// A var, not a const, only so a test can shrink it (Task 2's
// export_test.go seam); nothing at runtime writes it.
var registryFeedPageSize = 1000

type feedCursor struct {
	UpdateID *int64    // nil → ask by Since instead
	Since    time.Time // used only while UpdateID is nil
	Size     int       // 0 → registryFeedPageSize
}

type feedEntry struct {
	UpdateID           int64
	Date               time.Time
	OrganisationNumber string
	ChangeType         string // Ny | Endring | Sletting | Fjernet | Ukjent
}

type feedPage struct{ Entries []feedEntry }

func (c *brregClient) updates(ctx context.Context, cursor feedCursor) (feedPage, error)
```
  plus `var registryFeedPageSize = 1000` (a var so Task 2's test seam can shrink it).
  `updates` returns an error wrapping `errBrregUnavailable` for a transport failure, an exhausted retryable status, a non-2xx status, a non-JSON content type, a body over the cap, and a body that will not decode (a malformed `dato` included). An empty result — no `_embedded` at all — is `feedPage{}` with a nil error, never an error.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/customers/brreg_feed_test.go`. It is in package `customers_test` and reuses `registryTransport`/`registryEntityResponse` from `registry_test.go`, plus one transport of its own that records the full URL (path *and* query — the cursor is in the query string, which `registryTransport` deliberately drops):

```go
package customers_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the Brreg client's second read (registry workers design D1):
// GET /enhetsregisteret/api/oppdateringer/enheter, the incremental update
// feed the feed worker walks. Every body here was recorded from the live API
// on 2026-09-22 and trimmed to the fields this module reads; no test opens a
// socket — modtest.WithTransport is the seam, as brreg_test.go's own fake is.

// feedTransport answers by the request's full URL (path plus query), because
// the cursor a test is asserting on lives in the query string and
// registryTransport records only the path.
type feedTransport struct {
	mu      sync.Mutex
	urls    []string
	respond func(url string) (*http.Response, error)
}

func (f *feedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.urls = append(f.urls, r.URL.RequestURI())
	respond := f.respond
	f.mu.Unlock()
	return respond(r.URL.RequestURI())
}

func (f *feedTransport) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

// jsonFeedResponse is a feed response under the content type the live API
// answers with (plain application/json, not the entity endpoint's pinned v2
// media type — the feed negotiates nothing).
func jsonFeedResponse(status int, body string) *http.Response {
	return jsonResponse(status, body)
}

// feedPageBody is one recorded page: three entries, ascending
// oppdateringsid with the registry's own gaps, one of each interesting
// endringstype.
const feedPageBody = `{
	"_embedded": {"oppdaterteEnheter": [
		{"oppdateringsid": 25255241, "dato": "2026-09-21T04:05:11.193Z", "organisasjonsnummer": "929745760", "endringstype": "Endring",
		 "_links": {"enhet": {"href": "https://data.brreg.no/enhetsregisteret/api/enheter/929745760"}}},
		{"oppdateringsid": 25255244, "dato": "2026-09-21T04:06:02.001Z", "organisasjonsnummer": "923609016", "endringstype": "Ny",
		 "_links": {"enhet": {"href": "https://data.brreg.no/enhetsregisteret/api/enheter/923609016"}}},
		{"oppdateringsid": 25255250, "dato": "2026-09-21T04:07:44.500Z", "organisasjonsnummer": "974760673", "endringstype": "Fjernet"}
	]},
	"_links": {"self": {"href": "https://data.brreg.no/enhetsregisteret/api/oppdateringer/enheter?oppdateringsid=25255241&size=1000"}},
	"page": {"size": 1000, "totalElements": 3, "totalPages": 1, "number": 0}
}`

// emptyFeedBody is what the live API answers when nothing matched: no
// _embedded key at all, and page.totalElements 0. Decoding this as "zero
// entries" rather than as a malformed body is the one thing a reasonable
// implementer gets wrong here.
const emptyFeedBody = `{
	"_links": {"self": {"href": "https://data.brreg.no/enhetsregisteret/api/oppdateringer/enheter?dato=2026-09-22T00:00:00.000Z&size=1000"}},
	"page": {"size": 1000, "totalElements": 0, "totalPages": 0, "number": 0}
}`

func newFeedHarness(t *testing.T, transport http.RoundTripper) *modtest.Harness {
	t.Helper()
	return newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
}
```

The assertions themselves go through the exported test seam the next step adds, so the client can be driven without an HTTP handler. Add to `apps/server/internal/customers/export_test.go`:

```go
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
		out = append(out, FeedEntryForTest{UpdateID: e.UpdateID, Date: e.Date, OrganisationNumber: e.OrganisationNumber, ChangeType: e.ChangeType})
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
```
(`export_test.go` then also imports `context`, `time` and `github.com/vantigo-io/vantigo/server/internal/module`.)

Now the tests, appended to `brreg_feed_test.go`:

```go
// TestBrregFeed_AsksByDateUntilThereIsACursor pins the bootstrap request
// (design D1): with no stored oppdateringsid the feed is joined at a moment —
// never from the beginning of time — as ?dato=<started_at> in the registry's
// own millisecond-and-Z format, with the page size this module asks for.
func TestBrregFeed_AsksByDateUntilThereIsACursor(t *testing.T) {
	t.Parallel()
	transport := &feedTransport{respond: func(string) (*http.Response, error) {
		return jsonFeedResponse(http.StatusOK, emptyFeedBody), nil
	}}
	h := newFeedHarness(t, transport)

	since := time.Date(2026, 9, 22, 6, 30, 0, 0, time.UTC)
	entries, err := customers.FeedPageForTest(context.Background(), h.Deps(), nil, since, 1000)
	if err != nil {
		t.Fatalf("updates: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries = %v, want none: an empty result has no _embedded at all", entries)
	}
	want := "/enhetsregisteret/api/oppdateringer/enheter?dato=2026-09-22T06%3A30%3A00.000Z&size=1000"
	if got := transport.requests(); len(got) != 1 || got[0] != want {
		t.Errorf("requests = %v, want exactly [%s]", got, want)
	}
}

// TestBrregFeed_AsksByCursorOnceThereIsOne pins the steady-state request and
// every field of a recorded page: ascending ids with the registry's gaps, the
// dato as an instant, and every endringstype carried through verbatim (the
// worker decides what they mean, the client does not).
func TestBrregFeed_AsksByCursorOnceThereIsOne(t *testing.T) {
	t.Parallel()
	transport := &feedTransport{respond: func(string) (*http.Response, error) {
		return jsonFeedResponse(http.StatusOK, feedPageBody), nil
	}}
	h := newFeedHarness(t, transport)

	cursor := int64(25255241)
	entries, err := customers.FeedPageForTest(context.Background(), h.Deps(), &cursor, time.Time{}, 1000)
	if err != nil {
		t.Fatalf("updates: %v", err)
	}
	want := "/enhetsregisteret/api/oppdateringer/enheter?oppdateringsid=25255241&size=1000"
	if got := transport.requests(); len(got) != 1 || got[0] != want {
		t.Errorf("requests = %v, want exactly [%s]", got, want)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if entries[0].UpdateID != 25255241 || entries[0].OrganisationNumber != "929745760" || entries[0].ChangeType != "Endring" {
		t.Errorf("entries[0] = %+v, want 25255241/929745760/Endring", entries[0])
	}
	if got := entries[0].Date.UTC(); !got.Equal(time.Date(2026, 9, 21, 4, 5, 11, 193000000, time.UTC)) {
		t.Errorf("entries[0].Date = %v, want 2026-09-21T04:05:11.193Z", got)
	}
	if entries[1].ChangeType != "Ny" || entries[2].ChangeType != "Fjernet" {
		t.Errorf("change types = %q/%q, want Ny/Fjernet", entries[1].ChangeType, entries[2].ChangeType)
	}
	if entries[2].UpdateID != 25255250 {
		t.Errorf("entries[2].UpdateID = %d, want 25255250 (the ids have gaps)", entries[2].UpdateID)
	}
}

// TestBrregFeed_TheCursorWinsOverTheDate pins the one-or-the-other rule: a
// stored cursor is exact, a date is only a starting point, so a request never
// carries both — sending both would let the registry pick which one it honours.
func TestBrregFeed_TheCursorWinsOverTheDate(t *testing.T) {
	t.Parallel()
	transport := &feedTransport{respond: func(string) (*http.Response, error) {
		return jsonFeedResponse(http.StatusOK, emptyFeedBody), nil
	}}
	h := newFeedHarness(t, transport)

	cursor := int64(42)
	if _, err := customers.FeedPageForTest(context.Background(), h.Deps(), &cursor,
		time.Date(2026, 9, 22, 6, 30, 0, 0, time.UTC), 1000); err != nil {
		t.Fatalf("updates: %v", err)
	}
	got := transport.requests()
	if len(got) != 1 || strings.Contains(got[0], "dato=") || !strings.Contains(got[0], "oppdateringsid=42") {
		t.Errorf("request = %v, want oppdateringsid=42 and no dato", got)
	}
}

// TestBrregFeed_RetriesAServerErrorAndThenSucceeds pins that the feed reuses
// the search's and the entity read's own retry policy (brreg.go): a 5xx is
// worth another attempt, and the backoff seam keeps the test instant.
func TestBrregFeed_RetriesAServerErrorAndThenSucceeds(t *testing.T) {
	t.Parallel()
	var n int
	transport := &feedTransport{respond: func(string) (*http.Response, error) {
		n++
		if n == 1 {
			return jsonFeedResponse(http.StatusInternalServerError, `{}`), nil
		}
		return jsonFeedResponse(http.StatusOK, feedPageBody), nil
	}}
	h := newFeedHarness(t, transport)

	cursor := int64(1)
	entries, err := customers.FeedPageForTest(context.Background(), h.Deps(), &cursor, time.Time{}, 1000)
	if err != nil {
		t.Fatalf("updates: %v", err)
	}
	if len(entries) != 3 || n != 2 {
		t.Errorf("entries = %d after %d attempts, want 3 after 2", len(entries), n)
	}
}

// TestBrregFeed_ReportsEveryUnusableAnswerAsUnavailable pins the error kinds
// one table at a time. Every one of them is "the feed could not be read", not
// "there are no updates": the worker must leave its cursor exactly where it
// was for all of them, which it can only do if none of them look like success.
func TestBrregFeed_ReportsEveryUnusableAnswerAsUnavailable(t *testing.T) {
	t.Parallel()
	oversized := `{"_embedded":{"oppdaterteEnheter":[` +
		strings.Repeat(`{"oppdateringsid":1,"dato":"2026-09-21T04:05:11.193Z","organisasjonsnummer":"923609016","endringstype":"Ny"},`, 40000) +
		`{"oppdateringsid":2,"dato":"2026-09-21T04:05:11.193Z","organisasjonsnummer":"923609016","endringstype":"Ny"}]}}`

	cases := []struct {
		name        string
		status      int
		body        string
		contentType string
	}{
		{name: "a bad request the registry never retries away", status: http.StatusBadRequest, body: `{"melding":"oppdateringsid er ugyldig"}`},
		{name: "an exhausted server error", status: http.StatusInternalServerError, body: `{}`},
		{name: "an HTML error page from a proxy", status: http.StatusOK, body: `<html>gateway</html>`, contentType: "text/html"},
		{name: "a body that will not decode", status: http.StatusOK, body: `{"_embedded":{"oppdaterteEnheter":[`},
		{name: "a malformed dato", status: http.StatusOK, body: `{"_embedded":{"oppdaterteEnheter":[{"oppdateringsid":1,"dato":"yesterday","organisasjonsnummer":"923609016","endringstype":"Ny"}]}}`},
		{name: "a body past the cap", status: http.StatusOK, body: oversized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			transport := &feedTransport{respond: func(string) (*http.Response, error) {
				resp := jsonFeedResponse(tc.status, tc.body)
				if tc.contentType != "" {
					resp.Header.Set("Content-Type", tc.contentType)
				}
				return resp, nil
			}}
			h := newFeedHarness(t, transport)
			cursor := int64(1)
			if _, err := customers.FeedPageForTest(context.Background(), h.Deps(), &cursor, time.Time{}, 1000); err == nil {
				t.Fatal("updates succeeded, want an error")
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestBrregFeed' ./internal/customers/
```
Expected: a build failure — `undefined: customers.FeedPageForTest`, `undefined: feedCursor` — then, once `export_test.go` compiles, failures on every case.

- [ ] **Step 3: Write `brreg_feed.go`**

```go
package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// This file is the Brreg client's third and last operation (registry workers
// design D1): Enhetsregisteret's incremental update feed, which answers "which
// entities changed" so the feed worker never has to ask about entities that
// did not.
//
// Two things about it are load-bearing and neither is obvious from the URL:
//
//  1. **oppdateringsid is inclusive.** "From and including", in the registry's
//     own words, so resuming from the last id already processed would re-read
//     that entry forever. The cursor this module stores is therefore always
//     "the last id processed PLUS ONE" (registry_feed_worker.go), and this file
//     sends whatever it is given without adjusting it — one place owns the
//     arithmetic, and it is the place that also decides a page was processed.
//  2. **An empty result has no _embedded key at all**, with page.totalElements
//     0. That is zero updates, not a malformed body — the same shape and the
//     same ruling brreg.go's search already applies to its own missing
//     _embedded. Treating it as an error would make an idle installation
//     retry forever and never advance.
//
// Unlike the entity read there is no media-type negotiation here: the feed
// answers plain application/json, so a status is checked before a content type
// rather than after (there is no 406 to explain).

const (
	// brregFeedPath is the update feed's collection
	// (GET {base}/enhetsregisteret/api/oppdateringer/enheter).
	brregFeedPath = "/enhetsregisteret/api/oppdateringer/enheter"

	// brregFeedMaxBodyBytes caps one page at 4 MiB. A page of 1000 entries is
	// ~200 KiB (the live feed's entries are about 200 bytes each), so this is
	// twenty times the page this module asks for: a response past it is not a
	// busier day at the registry, it is something to refuse rather than buffer.
	brregFeedMaxBodyBytes = 4 << 20

	// brregFeedDateLayout is the registry's own dato format, documented as
	// yyyy-MM-dd'T'HH:mm:ss.SSS'Z' — milliseconds and a literal Z, not
	// RFC 3339 in general: a value with an offset instead of Z is rejected.
	brregFeedDateLayout = "2006-01-02T15:04:05.000Z"
)

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

// path is the request path for this cursor.
func (c feedCursor) path() string {
	v := url.Values{}
	if c.UpdateID != nil {
		v.Set("oppdateringsid", strconv.FormatInt(*c.UpdateID, 10))
	} else {
		v.Set("dato", c.Since.UTC().Format(brregFeedDateLayout))
	}
	size := c.Size
	if size <= 0 {
		size = registryFeedPageSize
	}
	v.Set("size", strconv.Itoa(size))
	return brregFeedPath + "?" + v.Encode()
}

// feedEntry is one update the registry reported: which entity, when, and what
// kind of change. ChangeType is carried through verbatim — "Ny", "Endring",
// "Sletting" (struck from the register), "Fjernet" (removed from open data) and
// "Ukjent" (older entries) — because every one of them counts to the worker
// (design D1: the entity is re-read whole whatever the reason), so this file
// has no business narrowing the set or rejecting a value the registry adds
// later.
type feedEntry struct {
	UpdateID           int64
	Date               time.Time
	OrganisationNumber string
	ChangeType         string
}

// feedPage is one page of the feed. There is deliberately nothing else on it:
// the worker's stopping rule is a SHORT page (fewer entries than it asked for)
// plus its own page budget, not the response's _links.next — a cursor that
// only advances over entries actually processed cannot be led astray by a link.
type feedPage struct {
	Entries []feedEntry
}

// brregFeedWire is the response's own field names. Embedded is a pointer
// because its absence is the empty result (this file's header, point 2).
type brregFeedWire struct {
	Embedded *struct {
		Updated []brregFeedEntryWire `json:"oppdaterteEnheter"`
	} `json:"_embedded"`
}

type brregFeedEntryWire struct {
	UpdateID           int64  `json:"oppdateringsid"`
	Date               string `json:"dato"`
	OrganisationNumber string `json:"organisasjonsnummer"`
	ChangeType         string `json:"endringstype"`
}

// errBrregFeedBody marks the two body failures that are terminal rather than
// retryable, exactly as errBrregEntityBody does for the entity read: a
// response past brregFeedMaxBodyBytes, and a body that could not be read to
// the end. A registry answering four megabytes of nonsense will answer the
// same four megabytes next attempt too.
var errBrregFeedBody = errors.New("brreg: update feed response body could not be used")

// validateBrregFeedContentType requires a JSON content type. The live feed
// answers application/json; a HAL-flavoured "+json" subtype is accepted too,
// since the body IS HAL (it carries _links and page) and a registry that one
// day labels it as such would still be sending exactly what this file decodes.
// Anything else — an HTML error page from a proxy — is named in the error
// rather than handed to json.Unmarshal.
func validateBrregFeedContentType(contentType string) error {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
		return fmt.Errorf("brreg update feed response had content type %q, want application/json", contentType)
	}
	return nil
}

// parseBrregFeedPage decodes one page. A malformed dato is a malformed body,
// not a dropped entry: the date is what the hint is written from (design D2),
// and an entry whose date this module invented would make a record look
// fresher or staler than the registry said.
func parseBrregFeedPage(body []byte) (feedPage, error) {
	var wire brregFeedWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return feedPage{}, fmt.Errorf("brreg: update feed body could not be decoded: %w", err)
	}
	if wire.Embedded == nil {
		return feedPage{}, nil
	}
	entries := make([]feedEntry, 0, len(wire.Embedded.Updated))
	for _, e := range wire.Embedded.Updated {
		date, err := time.Parse(brregFeedDateLayout, e.Date)
		if err != nil {
			return feedPage{}, fmt.Errorf("brreg: malformed update feed date %q: %w", e.Date, err)
		}
		entries = append(entries, feedEntry{
			UpdateID:           e.UpdateID,
			Date:               date.UTC(),
			OrganisationNumber: strings.TrimSpace(e.OrganisationNumber),
			ChangeType:         strings.TrimSpace(e.ChangeType),
		})
	}
	return feedPage{Entries: entries}, nil
}

// updates reads one page of the feed from cursor, bounded overall by c.timeout
// and per attempt by brregAttemptTimeout, retrying a transport error or a
// retryable status (isRetryableStatus) with c.backoff's delay between attempts
// — the same policy c.lookup and c.entity use. Every failure wraps
// errBrregUnavailable, so the one caller has exactly one thing to decide: end
// the cycle and leave the cursor alone.
func (c *brregClient) updates(ctx context.Context, cursor feedCursor) (feedPage, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	path := cursor.path()

	var lastErr error
	for attempt := 0; attempt <= brregRetryAttempts; attempt++ {
		if attempt > 0 {
			if !waitBackoff(ctx, c.backoff(attempt)) {
				break
			}
		}
		status, contentType, body, err := c.feedAttempt(ctx, path)
		if err != nil {
			if errors.Is(err, errBrregFeedBody) {
				return feedPage{}, fmt.Errorf("%w: %w", errBrregUnavailable, err)
			}
			lastErr = err
			continue
		}
		if isRetryableStatus(status) {
			lastErr = fmt.Errorf("brreg responded %d", status)
			continue
		}
		if !isSuccessStatus(status) {
			// A 400 is the registry telling this module its own request was
			// wrong (an oppdateringsid past int32, say): another attempt cannot
			// improve it, and it is still "the feed could not be read".
			return feedPage{}, fmt.Errorf("%w: brreg responded %d", errBrregUnavailable, status)
		}
		if err := validateBrregFeedContentType(contentType); err != nil {
			return feedPage{}, fmt.Errorf("%w: %w", errBrregUnavailable, err)
		}
		page, perr := parseBrregFeedPage(body)
		if perr != nil {
			return feedPage{}, fmt.Errorf("%w: %w", errBrregUnavailable, perr)
		}
		return page, nil
	}
	return feedPage{}, fmt.Errorf("%w: %w", errBrregUnavailable, lastErr)
}

// feedAttempt performs one GET for path, bounded by brregAttemptTimeout,
// asking for application/json and reading the body up to
// brregFeedMaxBodyBytes+1 bytes — one byte past the cap, so a body exactly at
// the limit is accepted and one over it is refused rather than silently
// truncated into something that happens to parse (entityAttempt's own idiom).
func (c *brregClient) feedAttempt(ctx context.Context, path string) (status int, contentType string, body []byte, err error) {
	attemptCtx, cancel := context.WithTimeout(ctx, brregAttemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, brregFeedMaxBodyBytes+1))
	if err != nil {
		return 0, "", nil, fmt.Errorf("%w: %w", errBrregFeedBody, err)
	}
	if len(data) > brregFeedMaxBodyBytes {
		return 0, "", nil, fmt.Errorf("%w: brreg update feed response exceeded %d bytes", errBrregFeedBody, brregFeedMaxBodyBytes)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), data, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestBrregFeed' ./internal/customers/
```
Expected: PASS, six tests (the last one with six subtests).

- [ ] **Step 5: Prove the tests can fail**

Change `if wire.Embedded == nil { return feedPage{}, nil }` to `return feedPage{}, errors.New("no updates")` and confirm `TestBrregFeed_AsksByDateUntilThereIsACursor` goes red; restore. Change `v.Set("oppdateringsid", …)` to also set `dato` and confirm `TestBrregFeed_TheCursorWinsOverTheDate` goes red; restore.

- [ ] **Step 6: Vet, lint, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go vet ./internal/customers/ && mise exec -- golangci-lint run ./internal/customers/... && mise exec -- go test -count=1 ./internal/customers/
cd /home/anders/projects/vantigo/vantigo
git add apps/server/internal/customers/brreg_feed.go apps/server/internal/customers/brreg_feed_test.go apps/server/internal/customers/export_test.go
printf '%s\n\n%s\n' 'feat(customers): the Brreg client reads which entities changed' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-task1
git commit -F /tmp/msg-task1 -- apps/server/internal/customers/brreg_feed.go apps/server/internal/customers/brreg_feed_test.go apps/server/internal/customers/export_test.go
git show --stat HEAD && git status --short
```

---

### Task 2: The cursor, its queries and the feed worker (D1, D2, D3, D4, D5)

**Files:**
- Create: `apps/server/internal/db/migrations/00023_customers_registry_feed.sql`, `apps/server/internal/customers/queries/registry_feed.sql`, `apps/server/internal/customers/registry_feed_worker.go`, `apps/server/internal/customers/registry_feed_worker_test.go`
- Modify: `apps/server/internal/customers/sqlc.yaml` (the `schema:` list), `apps/server/internal/db/schema_test.go` (`wantTables` in `TestCustomersBaseline_AppliesAndIsIdempotent`), `apps/server/internal/customers/export_test.go` (the two lease keys)
- Generated: `apps/server/internal/customers/store/*.go`
- Read first (do not change): `internal/communications/retention.go:110-190` (the loop and `underLease` this worker reproduces), `registry.go:615-700` (`refreshRegistryRecord`), `registry.go:431-439` (`registryOrganisationNumber`), `customers.go:115-123` (`identityFromRow`), `actor.go:38-40`

**Interfaces:**
- Consumes: `(*brregClient).updates` and `feedCursor`/`feedEntry`/`feedPage` (Task 1); `(*server).refreshRegistryRecord(ctx, customerID int32, orgnr, legalName string, act actor) (registryRefreshResult, error)`; `newServer(d module.Deps) *server`; `registryOrganisationNumber(identity *legalIdentity, customerType string) string`; `identityFromRow(legalCountry, legalID, legalName, legalSource, legalType *string) *legalIdentity`; `generatedFallbackActor`; `registryErrorKind(err) string`; `deref(*string) string`; `db.WithTx`; `store.New`.
- Produces:
```go
const (
	registryFeedWorkerName        = "customers-registry-feed"
	registryFeedLeaseKey    int64 = 0x4355535452454731 // "CUSTREG1"
	registryFeedPageBudget        = 20
	registryFeedStaleBatch        = 50
	registryFeedBackfillBatch     = 25
	defaultRegistryFeedPoll       = 15 * time.Minute
)

type RegistryFeedWorker struct { /* deps, srv */ }

func NewRegistryFeedWorker(d module.Deps) *RegistryFeedWorker
func (w *RegistryFeedWorker) Name() string                       // registryFeedWorkerName
func (w *RegistryFeedWorker) Interval() time.Duration            // Config.CustomersRegistryFeedPoll, else defaultRegistryFeedPoll
func (w *RegistryFeedWorker) Run(ctx context.Context) error
func (w *RegistryFeedWorker) RunCycle(ctx context.Context) (bool, error) // false = the lease is held elsewhere
func (w *RegistryFeedWorker) Sweep(ctx context.Context) (int, error)     // refreshed count
func (w *RegistryFeedWorker) ReadFeed(ctx context.Context) (int, error)  // pages processed
```
  Task 3 owns `Config.CustomersRegistryFeedPoll`; until it exists `Interval()` reads only the default (the field reference is added in Task 3, and this task's `Interval()` is written to the default alone — see Step 5's note).

- [ ] **Step 1: Write the migration**

Create `apps/server/internal/db/migrations/00023_customers_registry_feed.sql`:

```sql
-- +goose Up
-- Where this installation has read Brreg's update feed up to (registry
-- workers design D1): exactly one row, forever, which is why the primary key
-- is a constant rather than an identity — there is one feed and one position
-- in it, and a second row would silently mean two workers disagreeing about
-- what has been processed.
--
-- next_update_id is "the id to ask for next", already incremented past the
-- last entry processed: the registry's oppdateringsid parameter is INCLUSIVE
-- ("from and including"), so storing the last id itself would re-read that
-- entry on every cycle for the rest of time. NULL means the feed has never
-- been read, and the first request is made by started_at instead — the feed
-- is joined at the moment the worker first ran, never at the beginning of
-- time (the sweep, not the feed, is what covers the customers that existed
-- before then).
--
-- last_update_at is the dato of the last entry processed and last_polled_at
-- the last time a cycle read the feed at all: neither is read by any code
-- path, and both are here because the only report this delivery gives an
-- operator is a log line, and these two rows answer "is it running" and "how
-- far behind is it" from psql alone.
CREATE TABLE customers.registry_feed_cursor (
    id              smallint     PRIMARY KEY CHECK (id = 1),
    next_update_id  bigint,
    started_at      timestamptz  NOT NULL,
    last_polled_at  timestamptz,
    last_update_at  timestamptz
);

-- +goose Down
DROP TABLE customers.registry_feed_cursor;
```

Add `      - ../db/migrations/00023_customers_registry_feed.sql` as the last entry of `schema:` in `apps/server/internal/customers/sqlc.yaml`, and `"registry_feed_cursor",` as the last entry of `wantTables` in `internal/db/schema_test.go`'s `TestCustomersBaseline_AppliesAndIsIdempotent` (the list is compared against `ORDER BY table_name`, and `registry_feed_cursor` sorts after every `customers*` table).

- [ ] **Step 2: Write the queries**

Create `apps/server/internal/customers/queries/registry_feed.sql`:

```sql
-- name: EnsureRegistryFeedCursor :exec
-- EnsureRegistryFeedCursor plants the one cursor row the first time a cycle
-- runs (registry workers design D1), stamping the moment this installation
-- joined the feed. ON CONFLICT DO NOTHING rather than an upsert: started_at
-- is the feed's own starting point and must never move, or a replica that
-- started later would re-join the feed after the updates an earlier one had
-- already seen but not yet processed.
INSERT INTO customers.registry_feed_cursor (id, started_at)
VALUES (1, @started_at::timestamptz)
ON CONFLICT (id) DO NOTHING;

-- name: GetRegistryFeedCursor :one
-- GetRegistryFeedCursor is where to resume from. Read under the worker's
-- advisory lease and nowhere else, so it needs no lock of its own: the lease
-- is what makes one replica at a time the only reader and writer of this row.
SELECT id, next_update_id, started_at, last_polled_at, last_update_at
FROM customers.registry_feed_cursor
WHERE id = 1;

-- name: AdvanceRegistryFeedCursor :exec
-- AdvanceRegistryFeedCursor is the commit of one processed page (design D1):
-- run only after every matched customer on that page has had its hint written
-- and its refresh attempted, so a cycle that dies halfway re-reads the same
-- page rather than skipping it. next_update_id is the page's highest id PLUS
-- ONE, because oppdateringsid is inclusive.
UPDATE customers.registry_feed_cursor
SET next_update_id = @next_update_id,
    last_update_at = @last_update_at::timestamptz,
    last_polled_at = @last_polled_at::timestamptz
WHERE id = 1;

-- name: TouchRegistryFeedCursor :exec
-- TouchRegistryFeedCursor records that a cycle read the feed and found
-- nothing to process. It deliberately moves neither next_update_id nor
-- last_update_at: there was no entry, so there is no new position and no new
-- registry timestamp to claim — only the fact that this installation is still
-- asking, which is what tells an operator the worker is alive on an idle day.
UPDATE customers.registry_feed_cursor
SET last_polled_at = @last_polled_at::timestamptz
WHERE id = 1;

-- name: SetRegistryUpdatedHint :exec
-- SetRegistryUpdatedHint is "the feed said there is something newer" (design
-- D2), written on the record row BEFORE the refresh is attempted and in its
-- own statement, so that a refresh which then fails leaves hint > fetched_at
-- — the single definition of stale, and the only reason
-- StaleRegistryRecords below finds it again.
--
-- The hint never moves backwards: an older entry arriving after a newer one
-- (a page processed out of order, a backlog caught up in two cycles) must not
-- make a record look fresher than the registry said it was. A customer with
-- no record row has nowhere to keep a hint, and this UPDATE touching no row
-- is exactly right for that case — the refresh is still attempted, and the
-- backfill below is what retries it if that fails.
UPDATE customers.customer_registry_records
SET registry_updated_hint = @hint::timestamptz
WHERE customer_id = @customer_id
  AND (registry_updated_hint IS NULL OR registry_updated_hint < @hint::timestamptz);

-- name: CustomersByOrganisationNumbers :many
-- CustomersByOrganisationNumbers is one feed page intersected with this
-- installation (design D1): the unfiltered scan is matched LOCALLY, so the
-- whole register's churn costs one request and one indexed lookup rather than
-- a filtered request per chunk with no safe cursor.
--
-- The predicates are the same three registryOrganisationNumber applies in Go
-- (Norwegian business, non-archived), minus the organisation number's own
-- validity, which SQL cannot judge — the caller re-asks Go for that, so a
-- legacy legal_id like 'NO 923 609 016 MVA' is never refreshed for.
SELECT c.id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type
FROM customers.customers c
WHERE c.status <> 'archived'
  AND c.type = 'business'
  AND c.legal_country = 'no'
  AND c.legal_id = ANY(@legal_ids::text[])
ORDER BY c.id;

-- name: StaleRegistryRecords :many
-- StaleRegistryRecords is the sweep's first half (design D3): records the feed
-- said were out of date and whose refresh has not succeeded since. hint >
-- fetched_at IS the definition of stale — a successful refresh leaves
-- fetched_at at or past the hint, a failed one leaves the hint standing — so
-- this needs no separate retry ledger and no attempt counter: the row itself
-- remembers.
--
-- Oldest hint first, so a record that has been failing longest is asked about
-- before one that just changed. The organisation-number equality is the same
-- belt-and-braces RegistryAttentionCandidates applies: a row that outlived
-- the identity it was fetched for is nobody's to refresh.
SELECT r.customer_id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type
FROM customers.customer_registry_records r
JOIN customers.customers c ON c.id = r.customer_id
WHERE r.registry_updated_hint IS NOT NULL
  AND r.registry_updated_hint > r.fetched_at
  AND c.status <> 'archived'
  AND c.type = 'business'
  AND c.legal_country = 'no'
  AND r.organisation_number = c.legal_id
ORDER BY r.registry_updated_hint, r.customer_id
LIMIT @row_limit;

-- name: CustomersWithoutRegistryRecord :many
-- CustomersWithoutRegistryRecord is the sweep's second half — the backfill
-- (design D3): Norwegian business customers that have no record at all,
-- because they were created before delivery A existed or because their first
-- fetch failed. Lowest id first, so the order is stable and a large
-- installation drains front to back rather than re-reading the same few rows.
--
-- A backfilled record goes through the ordinary first-fetch diff, so a
-- hand-typed name that differs from the registry's raises registryRenamed
-- exactly as a click would — this query is not a special path, it only finds
-- the customers nobody has clicked for.
SELECT c.id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type
FROM customers.customers c
LEFT JOIN customers.customer_registry_records r ON r.customer_id = c.id
WHERE r.customer_id IS NULL
  AND c.status <> 'archived'
  AND c.type = 'business'
  AND c.legal_country = 'no'
  AND c.legal_id IS NOT NULL
ORDER BY c.id
LIMIT @row_limit;
```

Then generate, from `apps/server`:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```
Expected: `store/registry_feed.sql.go` appears on the first run and the second run adds nothing new. Confirm the generated parameter types: `CustomersByOrganisationNumbersParams` is not generated (a single `[]string` argument), `StaleRegistryRecordsParams` does not exist either (a single `RowLimit int32` argument), `SetRegistryUpdatedHintParams{Hint time.Time; CustomerID int32}` and `AdvanceRegistryFeedCursorParams{NextUpdateID *int64; LastUpdateAt time.Time; LastPolledAt time.Time}` do. **Read the generated file and use whatever it actually produced** — if sqlc named or shaped a parameter differently, the worker below follows sqlc, not this plan.

- [ ] **Step 3: Write the failing worker tests**

Create `apps/server/internal/customers/registry_feed_worker_test.go`. It reuses `feedTransport` (Task 1), `registryEntityResponse`, `equinorRegistryBody`, `movedEquinorRegistryBody`, `deletedRegistryBody`, `removedRegistryBody`, `createBrregPick`, `createCustomerWithIdentity`, `registryRowCount`, `fetchRegistryEvents`, `authenticatedClient` and `zeroBackoff` from the existing test files in this package.

```go
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

// entityBodies answers the entity read by organisation number, so one test can
// have one company change and another be removed.
func entityBodies(byOrgNumber map[string]*http.Response) func(string) (*http.Response, error) {
	return func(uri string) (*http.Response, error) {
		for orgnr, resp := range byOrgNumber {
			if strings.HasSuffix(uri, "/enhetsregisteret/api/enheter/"+orgnr) {
				return resp, nil
			}
		}
		return registryEntityResponse(http.StatusNotFound, ``), nil
	}
}

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

// registryHint is the stored hint for a customer, nil when there is none.
func registryHint(t *testing.T, h *modtest.Harness, customerID int32) *time.Time {
	t.Helper()
	return modtest.One[*time.Time](t, h,
		`SELECT registry_updated_hint FROM customers.customer_registry_records WHERE customer_id = $1`, customerID)
}

// registryFetchedAt is the stored fetched_at for a customer.
func registryFetchedAt(t *testing.T, h *modtest.Harness, customerID int32) time.Time {
	t.Helper()
	return modtest.One[time.Time](t, h,
		`SELECT fetched_at FROM customers.customer_registry_records WHERE customer_id = $1`, customerID)
}
```

The cases, all in the same file:

```go
// TestRegistryFeedWorker_BootstrapsByDateThenByCursor pins design D1's two
// request forms and the exact arithmetic between them: the first cycle has no
// cursor and joins the feed by date, and every cycle after it asks for the
// last id processed PLUS ONE, because oppdateringsid is inclusive. Getting
// that +1 wrong is invisible in production except as one entry re-read
// forever, which is why it is asserted on the URL and not only on the row.
func TestRegistryFeedWorker_BootstrapsByDateThenByCursor(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]*http.Response{}),
		feedPageOf(feedEntryOf(25255241, "2026-09-21T04:05:11.193Z", "929745760", "Endring")),
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
	if lastUpdate == nil || !lastUpdate.UTC().Equal(time.Date(2026, 9, 21, 4, 5, 11, 193000000, time.UTC)) {
		t.Errorf("last_update_at = %v, want the last entry's dato", lastUpdate)
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
		entityBodies(map[string]*http.Response{
			"923609016": registryEntityResponse(http.StatusOK, movedEquinorRegistryBody),
		}),
		feedPageOf(
			feedEntryOf(100, "2026-09-21T04:05:00.000Z", "929745760", "Endring"),
			feedEntryOf(101, "2026-09-21T04:06:00.000Z", "923609016", "Endring"),
		),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

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
	// movedEquinorRegistryBody differs from the create hook's body in two
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
		entityBodies(map[string]*http.Response{
			"923609016": registryEntityResponse(http.StatusOK, movedEquinorRegistryBody),
		}),
		feedPageOf(
			feedEntryOf(200, "2026-09-21T04:05:00.000Z", "923609016", "Endring"),
			feedEntryOf(201, "2026-09-21T09:30:00.000Z", "923609016", "Endring"),
		),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	before := len(entityRequests(transport))

	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if got := len(entityRequests(transport)) - before; got != 1 {
		t.Errorf("entity reads during the cycle = %d, want exactly 1 for a customer named twice on one page", got)
	}
	// The hint written before the refresh is the NEWEST of the customer's
	// entries, and the successful refresh leaves fetched_at at or past it.
	if fetched, hint := registryFetchedAt(t, h, created.Id), registryHint(t, h, created.Id); hint != nil && fetched.Before(*hint) {
		t.Errorf("fetched_at %v is before the hint %v after a successful refresh", fetched, *hint)
	}
}

// TestRegistryFeedWorker_AFailedFeedRequestLeavesTheCursorAlone pins design
// D1's failure rule. A cursor advanced past a page that was never read is a
// silent, permanent hole in the record: those entities changed, nothing here
// knows, and nothing ever will.
func TestRegistryFeedWorker_AFailedFeedRequestLeavesTheCursorAlone(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		entityBodies(map[string]*http.Response{}),
		feedPageOf(feedEntryOf(300, "2026-09-21T04:05:00.000Z", "929745760", "Endring")),
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
		entityBodies(map[string]*http.Response{}),
		feedPageOf(feedEntryOf(400, "2026-09-21T04:05:00.000Z", "929745760", "Endring")),
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
	transport := newRegistryWorkerTransport(entityBodies(map[string]*http.Response{}))
	transport.feed.respond = func(uri string) (*http.Response, error) {
		if !strings.HasPrefix(uri, "/enhetsregisteret/api/oppdateringer/enheter") {
			return registryEntityResponse(http.StatusNotFound, ``), nil
		}
		id++
		return jsonResponse(http.StatusOK, feedPageOf(
			feedEntryOf(id, "2026-09-21T04:05:00.000Z", "929745760", "Endring"))), nil
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
		entityBodies(map[string]*http.Response{
			"923609016": registryEntityResponse(http.StatusGone, removedRegistryBody),
		}),
		feedPageOf(feedEntryOf(600, "2026-09-21T04:05:00.000Z", "923609016", "Fjernet")),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	// The create hook stores the record first, so there is a copy to remove.
	transport.entity = entityBodies(map[string]*http.Response{
		"923609016": registryEntityResponse(http.StatusOK, equinorRegistryBody),
	})
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")
	if n := registryRowCount(t, h, created.Id); n != 1 {
		t.Fatalf("registry rows after create = %d, want 1", n)
	}
	transport.entity = entityBodies(map[string]*http.Response{
		"923609016": registryEntityResponse(http.StatusGone, removedRegistryBody),
	})

	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if n := registryRowCount(t, h, created.Id); n != 0 {
		t.Errorf("registry rows = %d, want 0: a 410 deletes the copy", n)
	}
	events := fetchRegistryEvents(t, c, created.Id)
	var removed int
	for _, e := range events {
		if strings.Contains(str(e.Summary), "removedFromOpenData") {
			removed++
		}
	}
	if removed != 1 {
		t.Errorf("removal events = %d, want exactly 1", removed)
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
		entityBodies(map[string]*http.Response{
			"923609016": registryEntityResponse(http.StatusOK, equinorRegistryBody),
		}),
		feedPageOf(feedEntryOf(700, "2026-09-21T04:05:00.000Z", "923609016", "Endring")),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

	// The feed still answers; the entity read does not.
	transport.entity = func(string) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, `{}`), nil
	}
	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	hint, fetched := registryHint(t, h, created.Id), registryFetchedAt(t, h, created.Id)
	if hint == nil || !hint.After(fetched) {
		t.Fatalf("hint = %v, fetched_at = %v; want hint > fetched_at after a failed refresh", hint, fetched)
	}

	// The registry comes back, and the next cycle's sweep — which reads no feed
	// entry for this customer at all — refreshes it.
	transport.entity = entityBodies(map[string]*http.Response{
		"923609016": registryEntityResponse(http.StatusOK, movedEquinorRegistryBody),
	})
	h.Advance(time.Minute)
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("second RunCycle: %v", err)
	}
	if hint, fetched := registryHint(t, h, created.Id), registryFetchedAt(t, h, created.Id); hint != nil && fetched.Before(*hint) {
		t.Errorf("still stale after the sweep: fetched_at %v, hint %v", fetched, *hint)
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
		entityBodies(map[string]*http.Response{
			"923609016": registryEntityResponse(http.StatusOK, equinorRegistryBody),
		}),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	// A manual identity: no create hook fetches for it (design D2), so this
	// customer has no record until the backfill finds it.
	created := createCustomerWithIdentity(t, c, "Equinor, typed by hand", "no", "923609016",
		map[string]any{"source": "manual"})
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

// TestRegistryFeedWorker_TheBackfillStopsAtItsBatch pins the bound the
// backfill needs to be safe on a large installation: a few hundred an hour,
// not every customer at once the first time the worker ever runs.
func TestRegistryFeedWorker_TheBackfillStopsAtItsBatch(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(
		func(string) (*http.Response, error) {
			return registryEntityResponse(http.StatusNotFound, ``), nil
		},
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	// 26 Norwegian business customers with valid organisation numbers and no
	// record: the batch is 25, so exactly one is left for the next cycle.
	for i := 0; i < 26; i++ {
		id := insertCustomer(t, h, fmt.Sprintf("Backfill %d", i), "active")
		h.Exec(t, `UPDATE customers.customers
			SET legal_country = 'no', legal_type = 'business', legal_source = 'manual',
			    legal_id = $2, legal_name = $3
			WHERE id = $1`, id, validOrgNumbers[i], fmt.Sprintf("Backfill %d", i))
	}

	w := customers.NewRegistryFeedWorker(h.Deps())
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	// A 404 stores nothing, so the count of entity reads is the only evidence
	// of how many the sweep actually attempted.
	if got := len(entityRequests(transport)); got != 25 {
		t.Errorf("entity reads = %d, want the 25-customer backfill batch", got)
	}
}

// TestRegistryFeedWorker_SkipsTheCycleWhenTheLeaseIsHeld is design D5 through
// this worker: a second replica logs and skips rather than reading the same
// pages and advancing the same cursor. Modelled on communications'
// TestRetentionWorker_SkipsTheCycleWhenTheLeaseIsHeld.
func TestRegistryFeedWorker_SkipsTheCycleWhenTheLeaseIsHeld(t *testing.T) {
	t.Parallel()
	transport := newRegistryWorkerTransport(entityBodies(map[string]*http.Response{}), feedPageOf())
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
	transport := newRegistryWorkerTransport(entityBodies(map[string]*http.Response{}), feedPageOf())
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
		entityBodies(map[string]*http.Response{
			"923609016": registryEntityResponse(http.StatusOK, movedEquinorRegistryBody),
		}),
		feedPageOf(feedEntryOf(800, "2026-09-21T04:05:00.000Z", "923609016", "Endring")),
		feedPageOf(),
	)
	h := newHarness(t, modtest.WithTransport(transport), modtest.WithBackoff(zeroBackoff))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

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
```

`validOrgNumbers` is the one fixture this file adds that is not derivable: 26 organisation numbers that pass `validNorwegianOrgNumber` (the MOD-11 check in `values.go`). They go in as literals, never computed at test time from the validator — a fixture derived from the code under test agrees with it even when it is wrong. Add to `registry_feed_worker_test.go`:

```go
// validOrgNumbers are 26 organisation numbers that pass the register's own
// MOD-11 check digit (values.go's validNorwegianOrgNumber), written out as
// literals rather than computed here: a fixture derived from the validator
// under test would agree with it even when it is wrong.
var validOrgNumbers = []string{
	"923609016", "974760673", "912660680", "929745760", "981276957",
	"998989684", "919115502", "915635857", "995339663", "988925519",
	"976389387", "914994552", "983974724", "948007029", "961329310",
	"918983388", "980430021", "983887457", "984851006", "992919119",
	"917127970", "914797262", "915442552", "989757481", "996171209",
	"920434934",
}
```
Verify the slice before relying on it, with a throwaway test in the same package that asserts every entry passes `validNorwegianOrgNumber` — run it once, confirm green, then delete it (it is a fixture check, not a behaviour):

```go
func TestValidOrgNumbersFixture(t *testing.T) {
	for _, n := range validOrgNumbers {
		if !customers.ValidNorwegianOrgNumberForTest(n) {
			t.Errorf("%s does not pass the check digit: replace it in the fixture", n)
		}
	}
}
```
with `func ValidNorwegianOrgNumberForTest(s string) bool { return validNorwegianOrgNumber(s) }` added to `export_test.go` and kept (the Task 4 worker tests need the same fixture). If any number fails, replace it with one that passes — the numbers above are real registered entities, but check them rather than trust them.

- [ ] **Step 4: Add the test seams to `export_test.go`**

```go
// RegistryFeedLeaseKeyForTest and PeppolRecheckLeaseKeyForTest are the two
// advisory-lease keys, exported so a test can take the same lock from a second
// connection and prove a cycle skips (design D5) — communications'
// RetentionLeaseKeyForTest is the same seam for the same reason.
const (
	RegistryFeedLeaseKeyForTest   = registryFeedLeaseKey
	PeppolRecheckLeaseKeyForTest  = peppolRecheckLeaseKey
)

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
```
`PeppolRecheckLeaseKeyForTest` is added in Task 4 with the worker it names — in **this** task `export_test.go` gets `RegistryFeedLeaseKeyForTest` and `SetRegistryFeedPageSize` only, so write the `const` block with the one key and add the second there. `registryFeedPageSize` is already a `var` (Task 1 declared it as one for exactly this seam), so nothing else changes.

- [ ] **Step 5: Run the tests to verify they fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestRegistryFeedWorker|TestValidOrgNumbersFixture' ./internal/customers/
```
Expected: a build failure — `undefined: customers.NewRegistryFeedWorker` — and then, once the worker exists, failures in every case.

- [ ] **Step 6: Write `registry_feed_worker.go`**

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the feed worker (registry workers design D1–D5): the thing that
// keeps a registry record current without anyone clicking Refresh.
//
// The three things about it a reasonable implementer would get wrong, all
// pinned by tests:
//
//  1. **The cursor is written AFTER a page is processed, and the id stored is
//     the page's last id PLUS ONE.** oppdateringsid is inclusive ("from and
//     including" — the registry's own docs), so storing the last id itself
//     re-reads that entry every cycle forever. Writing the cursor before the
//     page is processed is worse: those entities changed, this installation
//     skipped them, and nothing will ever say so again. A feed request that
//     fails must therefore leave the cursor exactly where it was, while a
//     *refresh* that fails must not — the hint below is what remembers that.
//  2. **The hint is written before the refresh, in its own statement.** A
//     successful refresh leaves fetched_at >= registry_updated_hint; a failed
//     one leaves hint > fetched_at, which is the single definition of stale and
//     the only reason the sweep ever finds it again (design D2, D3). Writing
//     the hint after the refresh, or inside its transaction, would make a
//     failed refresh indistinguishable from one that never happened.
//  3. **Every entry counts, including Ukjent, and none of them is the
//     authority.** The feed says *that* something changed, never what: the
//     entity is re-read whole through delivery A's own refresh path, which is
//     where a 410 becomes a deletion and a SlettetEnhet body becomes a
//     deletion date. A worker that mapped endringstype onto an outcome itself
//     would be a second, divergent copy of that ruling.
//
// The advisory lease (D5) is communications/retention.go's underLease,
// reproduced rather than shared: the two are the only users, and a shared
// helper would be a third module boundary to design for no benefit.

const (
	// registryFeedWorkerName is what the runner logs this worker as. It follows
	// the <module>-<worker> spelling communications' three workers established
	// (communications-outbox, communications-retention,
	// communications-attachment-cleanup) rather than the design doc's dotted
	// "customers.registry-feed": an operator greps one set of worker names, and
	// two spellings in one log would be the first thing to explain.
	registryFeedWorkerName = "customers-registry-feed"

	// registryFeedLeaseKey is the ASCII string "CUSTREG1" read as a big-endian
	// 64-bit value (design D5). Postgres advisory locks are per-database, so
	// this key shares one space with every other advisory-lock user in the
	// installation — which is why it is a recognisable constant rather than a
	// small number, and why the one-argument pg_try_advisory_lock(bigint)
	// overload is used rather than the two-argument one identity and energy
	// take for their own row-scoped locks.
	registryFeedLeaseKey int64 = 0x4355535452454731

	// registryFeedPageBudget is how many pages one cycle reads before stopping
	// (design D1). At registryFeedPageSize entries a page that is 20 000
	// entries a cycle — a week's churn of the whole register clears in two
	// cycles — while a worker that fell a month behind still never holds its
	// lease for an hour.
	registryFeedPageBudget = 20

	// registryFeedStaleBatch and registryFeedBackfillBatch bound the sweep
	// (design D3): up to 50 records whose refresh has not caught up with their
	// hint, and up to 25 customers that have no record at all. The second is
	// smaller on purpose — it is unbounded work the first time a worker ever
	// runs on an old installation, and a few hundred an hour catches a large
	// one up within a day without ever looking like an outage to the registry.
	registryFeedStaleBatch    = 50
	registryFeedBackfillBatch = 25

	// defaultRegistryFeedPoll is the cadence a worker built from a Deps with no
	// Config falls back on — the same value config.go defaults
	// CUSTOMERS_REGISTRY_FEED_POLL to, repeated here so a worker built from a
	// bare module.Deps is still well-defined rather than spinning.
	defaultRegistryFeedPoll = 15 * time.Minute
)

// RegistryFeedWorker reads Brreg's incremental update feed and re-reads the
// entities this installation is a customer of. It implements worker.Worker, so
// module.Workers hands it to cmd/vantigo's runner in worker mode and in api
// mode when WORKERS_IN_PROCESS=1.
type RegistryFeedWorker struct {
	deps module.Deps
	// srv is the module's own operations, built exactly as mount builds them:
	// the worker refreshes through refreshRegistryRecord — the same
	// network-then-transaction path a click takes, with the same Brreg client
	// from the same Deps.HTTPTransport seam — rather than reimplementing the
	// four outcomes beside it.
	srv *server
}

var _ worker.Worker = (*RegistryFeedWorker)(nil)

// NewRegistryFeedWorker builds the worker over d.
func NewRegistryFeedWorker(d module.Deps) *RegistryFeedWorker {
	return &RegistryFeedWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *RegistryFeedWorker) Name() string { return registryFeedWorkerName }

// Interval is the poll cadence between cycles (CUSTOMERS_REGISTRY_FEED_POLL,
// default 15 minutes). The feed is one small request per poll, so the cadence
// is about how soon a change is noticed, not about load.
func (w *RegistryFeedWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersRegistryFeedPoll > 0 {
		return w.deps.Config.CustomersRegistryFeedPoll
	}
	return defaultRegistryFeedPoll
}

// Run is the worker loop: run a cycle, sleep the poll interval, repeat until
// ctx is done. The interval is computed once, before the loop, as the retention
// worker computes it. A failing cycle is logged and the loop continues — the
// loop never dies, which is the property the runner depends on since it never
// restarts a worker.
func (w *RegistryFeedWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("registry feed cycle failed", "worker", registryFeedWorkerName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the advisory lease, reporting whether it ran:
// the sweep first, then the feed. false means another replica holds the lease
// and this one skipped, which is a normal, logged outcome and not an error.
//
// The sweep runs first because it is the half that only ever RETRIES work the
// feed has already accounted for (design D3): ordering it ahead means a cycle
// whose feed request fails still caught up whatever was outstanding, and a
// cycle that ends early leaves the cursor where the last fully processed page
// put it either way.
func (w *RegistryFeedWorker) RunCycle(ctx context.Context) (bool, error) {
	return w.underLease(ctx, func(ctx context.Context) error {
		if err := w.ensureCursor(ctx); err != nil {
			return err
		}
		if _, err := w.Sweep(ctx); err != nil {
			return err
		}
		_, err := w.ReadFeed(ctx)
		return err
	})
}

// underLease is design D5's non-blocking, installation-wide lease, the same
// shape communications' retention worker takes (retention.go's own underLease,
// whose comment explains the session scope and the WithoutCancel release in
// full). Reproduced rather than shared: this worker and the Peppol re-check
// worker beside it are the only two users in this module, and a shared helper
// would be a third module boundary to design.
func (w *RegistryFeedWorker) underLease(ctx context.Context, action func(context.Context) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("customers: acquire a connection for the registry feed lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, registryFeedLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("customers: take the registry feed lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the customers registry feed lease is held by another replica; skipping this cycle",
			"worker", registryFeedWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, registryFeedLeaseKey); err != nil {
			// A session that still holds the lease must never go back into the
			// pool: every later cycle in this process would draw it, find the
			// lock held by its own session, and skip silently forever. Closing
			// the connection makes Release destroy it.
			w.logger().Error("releasing the registry feed lease failed; discarding the connection",
				"worker", registryFeedWorkerName, "error", err)
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx)
}

// ensureCursor plants the cursor row the first time this installation runs a
// cycle, stamping the moment it joined the feed (design D1). Idempotent, and
// deliberately not an upsert: started_at must never move.
func (w *RegistryFeedWorker) ensureCursor(ctx context.Context) error {
	if err := store.New(w.deps.Pool).EnsureRegistryFeedCursor(ctx, w.now()); err != nil {
		return fmt.Errorf("customers: ensure the registry feed cursor: %w", err)
	}
	return nil
}

// Sweep is design D3: up to registryFeedStaleBatch records whose refresh has
// not caught up with their hint (oldest hint first), then up to
// registryFeedBackfillBatch Norwegian business customers with no record at all
// (lowest id first). It answers how many refreshes succeeded.
//
// A refresh that fails here is logged and left for the next cycle — never
// returned: one unreachable company must not stop the sweep from catching up
// on the other 74, and a stale row is still stale next cycle, which is the
// whole retry mechanism. A failure of the SELECTs themselves is returned: that
// is the database, not the registry.
//
// The sweep never touches the cursor. It is not reading the feed, so it has no
// position to advance, and advancing one on its behalf would claim pages nobody
// read.
func (w *RegistryFeedWorker) Sweep(ctx context.Context) (int, error) {
	q := store.New(w.deps.Pool)

	stale, err := q.StaleRegistryRecords(ctx, registryFeedStaleBatch)
	if err != nil {
		return 0, fmt.Errorf("customers: select stale registry records: %w", err)
	}
	refreshed := 0
	for _, row := range stale {
		if ctx.Err() != nil {
			return refreshed, nil
		}
		if w.refresh(ctx, row.CustomerID, row.Type, row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType) {
			refreshed++
		}
	}

	missing, err := q.CustomersWithoutRegistryRecord(ctx, registryFeedBackfillBatch)
	if err != nil {
		return refreshed, fmt.Errorf("customers: select customers without a registry record: %w", err)
	}
	for _, row := range missing {
		if ctx.Err() != nil {
			return refreshed, nil
		}
		if w.refresh(ctx, row.ID, row.Type, row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType) {
			refreshed++
		}
	}
	w.logger().Debug("registry sweep finished", "worker", registryFeedWorkerName,
		"stale", len(stale), "backfill", len(missing), "refreshed", refreshed)
	return refreshed, nil
}

// ReadFeed reads pages from the stored cursor until a page comes back short or
// the page budget is spent, answering how many pages it processed (design D1).
//
// A feed request that fails ends the cycle with the cursor untouched and the
// error returned, so the next cycle re-reads the same page. Everything a page
// costs — the hint, the refresh — happens before its cursor is written, so a
// page is either fully accounted for or read again.
func (w *RegistryFeedWorker) ReadFeed(ctx context.Context) (int, error) {
	q := store.New(w.deps.Pool)
	pages := 0
	for ; pages < registryFeedPageBudget; pages++ {
		if ctx.Err() != nil {
			return pages, nil
		}
		cursor, err := q.GetRegistryFeedCursor(ctx)
		if err != nil {
			return pages, fmt.Errorf("customers: read the registry feed cursor: %w", err)
		}
		page, err := w.srv.brreg.updates(ctx, feedCursor{
			UpdateID: cursor.NextUpdateID,
			Since:    cursor.StartedAt,
			Size:     registryFeedPageSize,
		})
		if err != nil {
			return pages, fmt.Errorf("customers: read the registry update feed: %w", err)
		}
		if len(page.Entries) == 0 {
			// Nothing to process and no position to claim: only the fact that
			// this installation is still asking.
			if err := q.TouchRegistryFeedCursor(ctx, w.now()); err != nil {
				return pages, fmt.Errorf("customers: record the registry feed poll: %w", err)
			}
			return pages, nil
		}
		if err := w.handlePage(ctx, q, page); err != nil {
			return pages, err
		}
		if len(page.Entries) < registryFeedPageSize {
			// A short page means the feed is caught up; one more request would
			// only ask the registry to say so again.
			return pages + 1, nil
		}
	}
	w.logger().Debug("registry feed page budget spent; the backlog continues next cycle",
		"worker", registryFeedWorkerName, "pages", pages)
	return pages, nil
}

// handlePage is one page: intersect it with this installation, write each
// matched customer's hint, refresh it, and only then advance the cursor.
//
// A customer named several times on one page is refreshed ONCE, with the newest
// of its entries as the hint (design D1, D2): the entity is re-read whole
// whatever the reason, so three requests would spend three requests to learn
// one thing, and the oldest of three timestamps would understate how current
// the record then is.
func (w *RegistryFeedWorker) handlePage(ctx context.Context, q *store.Queries, page feedPage) error {
	newest := make(map[string]time.Time, len(page.Entries))
	numbers := make([]string, 0, len(page.Entries))
	var highest int64
	for _, entry := range page.Entries {
		if entry.UpdateID > highest {
			// The feed is documented as monotonic ascending, so this is the last
			// entry's id in practice; taking the maximum explicitly means a page
			// that ever arrives out of order still cannot move the cursor
			// backwards over entries already processed.
			highest = entry.UpdateID
		}
		if entry.OrganisationNumber == "" {
			continue
		}
		if at, seen := newest[entry.OrganisationNumber]; !seen || entry.Date.After(at) {
			if !seen {
				numbers = append(numbers, entry.OrganisationNumber)
			}
			newest[entry.OrganisationNumber] = entry.Date
		}
	}

	matched, err := q.CustomersByOrganisationNumbers(ctx, numbers)
	if err != nil {
		return fmt.Errorf("customers: match a registry feed page: %w", err)
	}
	refreshed := 0
	for _, row := range matched {
		if ctx.Err() != nil {
			// Stop without advancing the cursor: this page is only partly
			// accounted for, so the next cycle must read it again.
			return nil
		}
		hint, ok := newest[deref(row.LegalID)]
		if !ok {
			continue
		}
		// Written BEFORE the refresh and in its own statement (design D2): a
		// refresh that fails must leave hint > fetched_at, which is what the
		// sweep retries on. A customer with no record row has nowhere to keep a
		// hint, and this statement touching no row is exactly right for it.
		if err := q.SetRegistryUpdatedHint(ctx, store.SetRegistryUpdatedHintParams{
			CustomerID: row.ID, Hint: hint,
		}); err != nil {
			return fmt.Errorf("customers: write a registry updated hint: %w", err)
		}
		if w.refresh(ctx, row.ID, row.Type, row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType) {
			refreshed++
		}
	}

	last := page.Entries[len(page.Entries)-1].Date
	next := highest + 1 // oppdateringsid is INCLUSIVE: see this file's header.
	if err := q.AdvanceRegistryFeedCursor(ctx, store.AdvanceRegistryFeedCursorParams{
		NextUpdateID: &next, LastUpdateAt: last, LastPolledAt: w.now(),
	}); err != nil {
		return fmt.Errorf("customers: advance the registry feed cursor: %w", err)
	}
	w.logger().Info("registry feed page processed", "worker", registryFeedWorkerName,
		"entries", len(page.Entries), "matched", len(matched), "refreshed", refreshed, "nextUpdateId", next)
	return nil
}

// refresh is one customer re-read through delivery A's own path with the system
// actor (design D4), reporting whether it succeeded. A failure is logged by
// kind — never the error text, which can carry the organisation number and the
// URL it was built from — and swallowed: the caller has more customers to get
// through, and the hint (or the missing record) is what remembers this one.
//
// A customer whose identity cannot be looked up at all — a legacy legal_id that
// is not an organisation number — is skipped silently: registryOrganisationNumber
// is the one rule for that, shared with the refresh endpoint's own 409.
func (w *RegistryFeedWorker) refresh(ctx context.Context, customerID int32, customerType string, legalCountry, legalID, legalName, legalSource, legalType *string) bool {
	identity := identityFromRow(legalCountry, legalID, legalName, legalSource, legalType)
	orgnr := registryOrganisationNumber(identity, customerType)
	if orgnr == "" {
		return false
	}
	if _, err := w.srv.refreshRegistryRecord(ctx, customerID, orgnr, identity.Name, generatedFallbackActor); err != nil {
		if errors.Is(err, errCustomerNotFound) || errors.Is(err, pgx.ErrNoRows) {
			// Archived or gone between the select and here: nothing to refresh
			// and nothing wrong.
			return false
		}
		w.logger().Warn("customers: registry record refresh failed",
			"worker", registryFeedWorkerName, "customerId", customerID, "errorKind", registryErrorKind(err))
		return false
	}
	return true
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers.
func (w *RegistryFeedWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *RegistryFeedWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
```

Two notes for the implementer:

- `Interval()` references `w.deps.Config.CustomersRegistryFeedPoll`, which Task 3 adds. Until then the field does not compile: write `Interval()` as `return defaultRegistryFeedPoll` in this task, and Task 3's Step 2 replaces it with the body above. Do not leave a commented-out field reference behind.
- `refresh`'s parameter list is long on purpose: every sweep and page row carries the same seven columns and every one of them is needed to re-derive the identity through the one shared rule. If sqlc named a returned column differently (`LegalID` vs `LegalId`), follow sqlc.

- [ ] **Step 7: Run the tests to verify they pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestRegistryFeedWorker|TestValidOrgNumbersFixture' ./internal/customers/ && mise exec -- go test -count=1 ./internal/customers/ ./internal/db/
```
Expected: PASS. Delete `TestValidOrgNumbersFixture` once it is green (keep `ValidNorwegianOrgNumberForTest`, Task 4 uses it).

- [ ] **Step 8: Prove the tests can fail**

Four guards, one at a time, each restored before the next:
1. `next := highest + 1` → `next := highest` — `TestRegistryFeedWorker_BootstrapsByDateThenByCursor` goes red on the cursor value and the second request's URL.
2. Move the `SetRegistryUpdatedHint` call below the `w.refresh(...)` call — `TestRegistryFeedWorker_AFailedRefreshLeavesTheHintForTheSweep` goes red.
3. In `ReadFeed`, move the `AdvanceRegistryFeedCursor` write ahead of `handlePage` (advance, then process) — `TestRegistryFeedWorker_AFailedFeedRequestLeavesTheCursorAlone` goes red.
4. Drop the `newest` map and refresh once per entry — `TestRegistryFeedWorker_OneCustomerWithTwoEntriesIsRefreshedOnce` goes red.

- [ ] **Step 9: Vet, lint, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go vet ./... && mise exec -- golangci-lint run ./internal/customers/... && mise exec -- go test -count=1 ./internal/customers/ ./internal/db/ ./internal/module/
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(customers): the update feed re-reads the registry for the customers it names' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-task2
git add apps/server/internal/db/migrations/00023_customers_registry_feed.sql apps/server/internal/customers/sqlc.yaml apps/server/internal/db/schema_test.go apps/server/internal/customers/queries/registry_feed.sql apps/server/internal/customers/store apps/server/internal/customers/registry_feed_worker.go apps/server/internal/customers/registry_feed_worker_test.go apps/server/internal/customers/export_test.go apps/server/internal/customers/brreg_feed.go apps/server/internal/customers/brreg_feed_test.go
git commit -F /tmp/msg-task2 -- apps/server/internal/db/migrations/00023_customers_registry_feed.sql apps/server/internal/customers/sqlc.yaml apps/server/internal/db/schema_test.go apps/server/internal/customers/queries/registry_feed.sql apps/server/internal/customers/store apps/server/internal/customers/registry_feed_worker.go apps/server/internal/customers/registry_feed_worker_test.go apps/server/internal/customers/export_test.go apps/server/internal/customers/brreg_feed.go apps/server/internal/customers/brreg_feed_test.go
git show --stat HEAD && git status --short
```

---

### Task 3: Configuration, and the worker actually starting (D7)

**Files:**
- Modify: `apps/server/internal/config/config.go` (five variables), `apps/server/internal/config/config_test.go`, `apps/server/internal/customers/module.go` (the `Workers` field), `apps/server/internal/customers/module_test.go`, `apps/server/internal/customers/registry_feed_worker.go` (`Interval()` reads its config), `deploy/compose/vantigo.env.example`
- Read first (do not change): `config.go:340-365` (the load order), `config.go:596-602` (`communicationsRetention`, the precedent), `config.go:640-680` (`integer`/`duration`/`boolean`/`flag`), `internal/communications/module.go:79-103` (how a module declares workers)

**Interfaces:**
- Produces, on `config.Config`:
```go
CustomersRegistryFeedEnabled  bool          // CUSTOMERS_REGISTRY_FEED_ENABLED, default true
CustomersRegistryFeedPoll     time.Duration // CUSTOMERS_REGISTRY_FEED_POLL, default 15m
CustomersPeppolRecheckEnabled bool          // CUSTOMERS_PEPPOL_RECHECK_ENABLED, default true
CustomersPeppolRecheckPoll    time.Duration // CUSTOMERS_PEPPOL_RECHECK_POLL, default 24h
CustomersPeppolRecheckAge     time.Duration // CUSTOMERS_PEPPOL_RECHECK_AGE, default 720h
```
  and, in `customers`, `func workers(d module.Deps) []worker.Worker` wired as `module.Module{… Workers: workers}`. Task 4 adds the Peppol worker to that function; this task registers the feed worker alone so the package compiles at every commit.

- [ ] **Step 1: Write the failing config and module tests**

Append to `apps/server/internal/config/config_test.go`:

```go
// TestLoad_CustomersRegistryWorkers pins the five variables the registry
// workers read (registry workers design D7). Both switches default ON: the
// workers are what make a registry record worth relying on, so an installation
// that says nothing gets them — the same "on unless turned off" default
// WORKERS_IN_PROCESS and PEPPOL_LOOKUP_ENABLED take, and the opposite of
// flag()'s fail-safe-off switches.
func TestLoad_CustomersRegistryWorkers(t *testing.T) {
	cfg := mustLoad(t, validEnv())
	if !cfg.CustomersRegistryFeedEnabled {
		t.Error("CUSTOMERS_REGISTRY_FEED_ENABLED unset did not default to on")
	}
	if cfg.CustomersRegistryFeedPoll != 15*time.Minute {
		t.Errorf("CustomersRegistryFeedPoll = %v, want 15m", cfg.CustomersRegistryFeedPoll)
	}
	if !cfg.CustomersPeppolRecheckEnabled {
		t.Error("CUSTOMERS_PEPPOL_RECHECK_ENABLED unset did not default to on")
	}
	if cfg.CustomersPeppolRecheckPoll != 24*time.Hour {
		t.Errorf("CustomersPeppolRecheckPoll = %v, want 24h", cfg.CustomersPeppolRecheckPoll)
	}
	if cfg.CustomersPeppolRecheckAge != 720*time.Hour {
		t.Errorf("CustomersPeppolRecheckAge = %v, want 720h", cfg.CustomersPeppolRecheckAge)
	}

	tuned := mustLoad(t, with(validEnv(),
		"CUSTOMERS_REGISTRY_FEED_ENABLED", "0",
		"CUSTOMERS_REGISTRY_FEED_POLL", "5m",
		"CUSTOMERS_PEPPOL_RECHECK_ENABLED", "0",
		"CUSTOMERS_PEPPOL_RECHECK_POLL", "6h",
		"CUSTOMERS_PEPPOL_RECHECK_AGE", "168h",
	))
	if tuned.CustomersRegistryFeedEnabled || tuned.CustomersPeppolRecheckEnabled {
		t.Error("the switches did not turn off")
	}
	if tuned.CustomersRegistryFeedPoll != 5*time.Minute || tuned.CustomersPeppolRecheckPoll != 6*time.Hour ||
		tuned.CustomersPeppolRecheckAge != 168*time.Hour {
		t.Errorf("durations = %v/%v/%v, want 5m/6h/168h",
			tuned.CustomersRegistryFeedPoll, tuned.CustomersPeppolRecheckPoll, tuned.CustomersPeppolRecheckAge)
	}
}

// TestLoad_CustomersRegistryWorkersRejectNonsense pins that the switches are
// the same strict "0"/"1" every other one is and the poll cadences are
// positive durations — a zero or negative poll would make a worker's ticker
// panic or spin, which is worse than refusing to start.
func TestLoad_CustomersRegistryWorkersRejectNonsense(t *testing.T) {
	if msg := loadError(t, with(validEnv(), "CUSTOMERS_REGISTRY_FEED_ENABLED", "yes")); !strings.Contains(msg, `CUSTOMERS_REGISTRY_FEED_ENABLED: must be "0" or "1"`) {
		t.Errorf("error = %q", msg)
	}
	if msg := loadError(t, with(validEnv(), "CUSTOMERS_PEPPOL_RECHECK_ENABLED", "yes")); !strings.Contains(msg, `CUSTOMERS_PEPPOL_RECHECK_ENABLED: must be "0" or "1"`) {
		t.Errorf("error = %q", msg)
	}
	for _, field := range []string{"CUSTOMERS_REGISTRY_FEED_POLL", "CUSTOMERS_PEPPOL_RECHECK_POLL", "CUSTOMERS_PEPPOL_RECHECK_AGE"} {
		if msg := loadError(t, with(validEnv(), field, "0s")); !strings.Contains(msg, field+": must be a positive duration such as 30s") {
			t.Errorf("%s: error = %q", field, msg)
		}
		if msg := loadError(t, with(validEnv(), field, "soon")); !strings.Contains(msg, field+": must be a positive duration such as 30s") {
			t.Errorf("%s: error = %q", field, msg)
		}
	}
}
```

Append to `apps/server/internal/customers/module_test.go` (it needs the imports `slices` — already there — plus `time`, `github.com/vantigo-io/vantigo/server/internal/module` and `github.com/vantigo-io/vantigo/server/internal/modtest`):

```go
// TestModule_ContributesItsWorkers proves the background workers this module
// owns are reachable the way production starts them — through Module().Workers,
// which module.Workers collects for cmd/vantigo's runner — and not only through
// the constructors the worker tests call directly.
//
// It is an EXACT-SET assertion. A worker fully implemented, fully tested and
// never registered is the failure this guards: communications learned that one
// the hard way (its own TestModule_ContributesItsWorkers says so), and
// **adding a worker to this module means adding its name here.** The interval
// check is part of the same guard — the runner logs a worker's cadence, and a
// zero interval would make a poll loop spin.
func TestModule_ContributesItsWorkers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	var names []string
	for _, w := range module.Workers(h.Deps(), customers.Module()) {
		names = append(names, w.Name())
		if w.Interval() <= 0 {
			t.Errorf("worker %s has interval %v, want a positive poll interval", w.Name(), w.Interval())
		}
	}
	slices.Sort(names)
	want := []string{"customers-registry-feed"}
	if !slices.Equal(names, want) {
		t.Errorf("workers = %v, want exactly %v", names, want)
	}
}

// TestModule_TheFeedWorkerCanBeTurnedOff pins design D7's switch where it
// actually has to hold: not merely as a config field, but as a worker the
// runner is never handed. An installation that sets the switch to 0 must make
// no outbound request to Brreg on a schedule at all, and the only way to
// promise that is not to start the thing that would.
func TestModule_TheFeedWorkerCanBeTurnedOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("CUSTOMERS_REGISTRY_FEED_ENABLED", "0"))
	if got := module.Workers(h.Deps(), customers.Module()); len(got) != 0 {
		t.Errorf("workers = %d with the feed disabled, want none", len(got))
	}
}

// TestModule_TheFeedWorkerPollCadenceIsConfigured pins that the runner-facing
// cadence is the operator's, not the constant's.
func TestModule_TheFeedWorkerPollCadenceIsConfigured(t *testing.T) {
	t.Parallel()
	if got := customers.NewRegistryFeedWorker(newHarness(t).Deps()).Interval(); got != 15*time.Minute {
		t.Errorf("Interval = %v, want the 15m default", got)
	}
	tuned := newHarness(t, modtest.WithEnv("CUSTOMERS_REGISTRY_FEED_POLL", "90s"))
	if got := customers.NewRegistryFeedWorker(tuned.Deps()).Interval(); got != 90*time.Second {
		t.Errorf("Interval = %v, want the configured 90s", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestLoad_CustomersRegistryWorkers' ./internal/config/ && mise exec -- go test -count=1 -run 'TestModule_' ./internal/customers/
```
Expected: `undefined: cfg.CustomersRegistryFeedEnabled` for the first, and `workers = [] want [customers-registry-feed]` for the second once config compiles.

- [ ] **Step 3: Add the five variables**

In `apps/server/internal/config/config.go`, add to the `Config` struct, immediately after `PeppolTimeout` (the customers module's own settings stay together):

```go
	// CustomersRegistryFeedEnabled is the on/off switch for the customers
	// module's Brreg update-feed worker (CUSTOMERS_REGISTRY_FEED_ENABLED,
	// registry workers design D7). Default ON: the worker is what keeps a
	// registry record, the dashboard's attention list and the billing warnings
	// from going quietly stale, so an installation that says nothing gets it.
	// A 0 means the worker is never handed to the runner at all, so no
	// scheduled request is made to Brreg.
	CustomersRegistryFeedEnabled bool
	// CustomersRegistryFeedPoll is how often that worker runs a cycle
	// (CUSTOMERS_REGISTRY_FEED_POLL, default 15 minutes). The feed is one small
	// request per poll, so this is about how soon a change is noticed rather
	// than about load.
	CustomersRegistryFeedPoll time.Duration
	// CustomersPeppolRecheckEnabled is the on/off switch for the Peppol
	// re-check worker (CUSTOMERS_PEPPOL_RECHECK_ENABLED, design D6, D7).
	// Effective only alongside PeppolLookupEnabled: with the lookup itself off
	// there is no network to ask, and the worker is not started either way.
	CustomersPeppolRecheckEnabled bool
	// CustomersPeppolRecheckPoll is how often that worker runs a cycle
	// (CUSTOMERS_PEPPOL_RECHECK_POLL, default 24 hours). A Peppol registration
	// is not a thing that changes hourly, and each cycle is up to a hundred
	// network lookups.
	CustomersPeppolRecheckPoll time.Duration
	// CustomersPeppolRecheckAge is how old a stored lookup must be before the
	// worker asks again (CUSTOMERS_PEPPOL_RECHECK_AGE, default 720 hours — 30
	// days). It is deliberately a duration rather than a day count, so an
	// installation can shorten it to something a test or a pilot can observe.
	CustomersPeppolRecheckAge time.Duration
```

Add the loader beside `peppolLookup`:

```go
// customersRegistryWorkers loads the two customers background workers'
// settings (registry workers design D7): each worker's switch and cadence, and
// the age at which a stored Peppol lookup is asked again. Both switches default
// on, like WORKERS_IN_PROCESS and PEPPOL_LOOKUP_ENABLED and unlike flag()'s
// fail-safe-off switches: these workers are the delivery, not an extra.
//
// Page size, page budget, sweep batches and the Peppol batch are constants in
// the workers themselves, not knobs (design D7): they bound one cycle's work
// against a public register, and an operator who could raise them could make
// this installation look like an attack.
func customersRegistryWorkers(p *problems, env map[string]string, c *Config) {
	c.CustomersRegistryFeedEnabled = boolean(p, env, "CUSTOMERS_REGISTRY_FEED_ENABLED", true)
	c.CustomersRegistryFeedPoll = duration(p, env, "CUSTOMERS_REGISTRY_FEED_POLL", 15*time.Minute)
	c.CustomersPeppolRecheckEnabled = boolean(p, env, "CUSTOMERS_PEPPOL_RECHECK_ENABLED", true)
	c.CustomersPeppolRecheckPoll = duration(p, env, "CUSTOMERS_PEPPOL_RECHECK_POLL", 24*time.Hour)
	c.CustomersPeppolRecheckAge = duration(p, env, "CUSTOMERS_PEPPOL_RECHECK_AGE", 720*time.Hour)
}
```

and call it in `Load`, on the line after `peppolLookup(&p, env, c)`:

```go
	peppolLookup(&p, env, c)
	customersRegistryWorkers(&p, env, c)
```

- [ ] **Step 4: Register the worker and read its cadence**

In `apps/server/internal/customers/module.go`, add the import `"github.com/vantigo-io/vantigo/server/internal/worker"`, the `Workers` field, and the function:

```go
func Module() module.Module {
	return module.Module{
		Name:        "customers",
		Permissions: permissions,
		Mount:       mount,
		Directory:   newDirectory,
		Workers:     workers,
	}
}

// workers is this module's background work (registry workers design D7): the
// Brreg update-feed worker. cmd/vantigo starts these through module.Workers in
// worker mode, and in api mode when WORKERS_IN_PROCESS=1 — never in server
// mode.
//
// Each worker is registered only when its own switch is on, rather than
// registered always and skipped inside its cycle: "the feed worker is off"
// must mean this process never makes that outbound request on a schedule, and
// the honest way to promise that is not to hand the runner the thing that
// would. The runner's startup log then names exactly the workers that are
// actually running, which is what an operator reads it for.
//
// Both workers take an advisory lease, unlike communications' outbox and
// cleanup workers: they have no per-row claim to fall back on — a cursor is
// one row for the whole installation, and a re-check is a network call with no
// row to claim first — so one replica at a time is the exclusion (design D5).
//
// TestModule_ContributesItsWorkers asserts this set exactly: a worker added
// here without its name added there, or the reverse, fails that test rather
// than silently never running in production.
func workers(d module.Deps) []worker.Worker {
	if d.Config == nil {
		// A Deps with no Config cannot say whether either worker is wanted, and
		// newServer below would dereference it. Nothing in production builds one;
		// a test that does gets no workers rather than a panic.
		return nil
	}
	var out []worker.Worker
	if d.Config.CustomersRegistryFeedEnabled {
		out = append(out, NewRegistryFeedWorker(d))
	}
	return out
}
```

In `registry_feed_worker.go`, replace Task 2's placeholder `Interval()` body with the configured one:

```go
func (w *RegistryFeedWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersRegistryFeedPoll > 0 {
		return w.deps.Config.CustomersRegistryFeedPoll
	}
	return defaultRegistryFeedPoll
}
```

- [ ] **Step 5: Document the variables for an operator**

In `deploy/compose/vantigo.env.example`, add a block directly after the `# --- Customers: Peppol lookup ---` block (so the customers settings stay together) — the commented-out form every other variable there takes:

```
# --- Customers: registry workers ----------------------------------------------
# Two background workers keep a customer's registry data current without
# anybody clicking Refresh. They run wherever this deployment already runs
# workers: in `api` mode with WORKERS_IN_PROCESS=1, and in `worker` mode —
# never in `server` mode. Both elect one replica per cycle through a Postgres
# advisory lock, so running several is safe.
#
# The feed worker reads Brreg's incremental update feed (one small request per
# poll, matched against this installation's customers locally) and re-reads the
# entities it names, through the same path the Refresh button uses. It also
# sweeps: records whose last refresh failed, and Norwegian business customers
# that have no registry record at all yet (customers created before the record
# existed, a few dozen per cycle until they are caught up).
# CUSTOMERS_REGISTRY_FEED_ENABLED=1
# CUSTOMERS_REGISTRY_FEED_POLL=15m
#
# The Peppol re-check worker asks the Peppol network again for customers whose
# stored answer has aged past CUSTOMERS_PEPPOL_RECHECK_AGE, and for customers
# already set to receive EHF invoices that have never been checked at all. It
# needs PEPPOL_LOOKUP_ENABLED=1 (above) — with the lookup off it never starts —
# and it never changes a customer's invoice delivery: a lapsed registration
# shows up as the billing profile's own warning, for a person to act on.
# CUSTOMERS_PEPPOL_RECHECK_ENABLED=1
# CUSTOMERS_PEPPOL_RECHECK_POLL=24h
# CUSTOMERS_PEPPOL_RECHECK_AGE=720h
```

`README.md` needs no change: its command table already says "every enabled module's background workers", which now includes these. `CONTRIBUTING.md` does name today's workers and is Task 6's.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 ./internal/config/ ./internal/customers/ ./internal/module/
```
Expected: PASS.

- [ ] **Step 7: Prove the tests can fail**

Set `Workers: nil` in `Module()` and confirm `TestModule_ContributesItsWorkers` goes red; restore. Change the `CustomersRegistryFeedEnabled` default to `false` and confirm `TestLoad_CustomersRegistryWorkers` goes red; restore. Drop the `if d.Config.CustomersRegistryFeedEnabled` guard and confirm `TestModule_TheFeedWorkerCanBeTurnedOff` goes red; restore.

- [ ] **Step 8: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go vet ./... && mise exec -- golangci-lint run ./internal/config/... ./internal/customers/...
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(customers): an installation can say how often the registry workers run, or that they do not' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-task3
git add apps/server/internal/config/config.go apps/server/internal/config/config_test.go apps/server/internal/customers/module.go apps/server/internal/customers/module_test.go apps/server/internal/customers/registry_feed_worker.go deploy/compose/vantigo.env.example
git commit -F /tmp/msg-task3 -- apps/server/internal/config/config.go apps/server/internal/config/config_test.go apps/server/internal/customers/module.go apps/server/internal/customers/module_test.go apps/server/internal/customers/registry_feed_worker.go deploy/compose/vantigo.env.example
git show --stat HEAD && git status --short
```

---

### Task 4: The Peppol re-check worker (D5, D6)

**Files:**
- Create: `apps/server/internal/customers/queries/peppol_recheck.sql`, `apps/server/internal/customers/peppol_recheck_worker.go`, `apps/server/internal/customers/peppol_recheck_worker_test.go`
- Modify: `apps/server/internal/customers/peppol_lookup.go` (extract `lookupAndStorePeppol`), `apps/server/internal/customers/module.go` (`workers` gains the second worker), `apps/server/internal/customers/module_test.go` (the exact set), `apps/server/internal/customers/export_test.go` (`PeppolRecheckLeaseKeyForTest`)
- Generated: `apps/server/internal/customers/store/*.go`
- Read first (do not change): `peppol_lookup.go:164-282` (the handler being extracted from), `billing_values.go:69-102` (`billingProfileFromRow`, `derivedPeppolID`), `peppol_lookup_test.go:37-88` (`stubPeppolLookup`, `peppolLookupCalls`, `createNorwegianBusiness`)

**Interfaces:**
- Consumes: `lookupParticipant(profile billingProfile, identity *legalIdentity, customerType string) (string, bool)`; `billingProfileFromRow(...)`; `identityFromRow(...)`; `peppolResultStatus(peppol.Result) string`; `peppolErrorKind(error) string`; `recordCustomerPeppolLookup(ctx, q, now, customerID, status, canInvoice, canCreditNote, smpHost, previousStatus, actorKind, actorDisplay, actorUserID)`; `store.UpsertCustomerPeppolLookupParams`; `generatedFallbackActor`.
- Produces:
```go
// in peppol_lookup.go
type peppolLookupOutcome struct {
	Status               string
	CanReceiveInvoice    bool
	CanReceiveCreditNote bool
	SMPHost              *string
	CheckedAt            time.Time
	Changed              bool
}

func (s *server) lookupAndStorePeppol(ctx context.Context, customerID int32, participant string, act actor) (peppolLookupOutcome, error)

// in peppol_recheck_worker.go
const (
	peppolRecheckWorkerName        = "customers-peppol-recheck"
	peppolRecheckLeaseKey    int64 = 0x4355535450455031 // "CUSTPEP1"
	peppolRecheckBatch             = 100
	defaultPeppolRecheckPoll       = 24 * time.Hour
	defaultPeppolRecheckAge        = 720 * time.Hour
)

type PeppolRecheckWorker struct { /* deps, srv */ }

func NewPeppolRecheckWorker(d module.Deps) *PeppolRecheckWorker
func (w *PeppolRecheckWorker) Name() string
func (w *PeppolRecheckWorker) Interval() time.Duration
func (w *PeppolRecheckWorker) Run(ctx context.Context) error
func (w *PeppolRecheckWorker) RunCycle(ctx context.Context) (bool, error)
```

- [ ] **Step 1: Extract `lookupAndStorePeppol` with the handler's tests as the guard**

This step is behaviour-preserving: **no test is added or changed**, and `mise exec -- go test -count=1 -run 'TestPostCustomersByIdPeppolLookup|TestBillingProfile' ./internal/customers/` must be green before and after. Run it first and note the count.

In `apps/server/internal/customers/peppol_lookup.go`, add the type and the function, lifting the handler's timeout, network call, failure log and transaction verbatim:

```go
// peppolLookupOutcome is one completed lookup-and-store: what the network
// said, when, and whether that was news. Changed is what decides the timeline
// event, and it is returned rather than kept private because the worker logs a
// cycle's summary from it (design D6) — the click does not need it, since the
// event is already written by the time it reads the outcome.
type peppolLookupOutcome struct {
	Status               string
	CanReceiveInvoice    bool
	CanReceiveCreditNote bool
	SMPHost              *string
	CheckedAt            time.Time
	Changed              bool
}

// lookupAndStorePeppol asks the Peppol network about participant and remembers
// the answer: the network call bounded by Config.PeppolTimeout and OUTSIDE any
// transaction, then one transaction with a locked read of the stored row, the
// upsert, and the customer.peppol_lookup event only when the answer changed
// (design D3's controller ruling, unchanged).
//
// It is one function because there are two callers and there must be exactly
// one ruling (registry workers design D6): POST .../peppol-lookup, where a
// person is waiting, and the re-check worker, where nobody is. The only thing
// the two do differently is what they do with the error — 502 versus a log
// line and the next customer — so the error is returned and the warning is
// logged here, once, in the shape both need.
//
// The caller must have decided the participant already (lookupParticipant) and
// resolved its actor before calling: an empty participant is a caller's bug,
// not an outcome, and actorFor is an out-of-process call that must not happen
// inside this function's transaction.
func (s *server) lookupAndStorePeppol(ctx context.Context, customerID int32, participant string, act actor) (peppolLookupOutcome, error) {
	lookupCtx := ctx
	if s.deps.Config.PeppolTimeout > 0 {
		var cancel context.CancelFunc
		lookupCtx, cancel = context.WithTimeout(ctx, s.deps.Config.PeppolTimeout)
		defer cancel()
	}
	result, err := s.peppolLookup(lookupCtx, participant)
	if err != nil {
		// The kind alone, never err.Error(): peppol.Client.Lookup's message can
		// carry the participant identifier, which is an organisation number and
		// does not belong in a log line next to the customer id that caused it.
		s.deps.Logger.WarnContext(ctx, "customers: peppol lookup failed", "customerId", customerID, "errorKind", peppolErrorKind(err))
		return peppolLookupOutcome{}, err
	}
	// Read once the network call has returned, not before it was made: the call
	// itself takes real time, and checkedAt is supposed to say when the answer
	// was obtained, not when it was asked for.
	now := s.deps.Clock()

	status := peppolResultStatus(result)
	var smpHost *string
	if result.SMPHost != "" {
		host := result.SMPHost
		smpHost = &host
	}

	var (
		previous    store.CustomersCustomerPeppolLookup
		hadPrevious bool
		changed     bool
	)
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var perr error
		previous, perr = txq.GetCustomerPeppolLookupForUpdate(ctx, customerID)
		switch {
		case errors.Is(perr, pgx.ErrNoRows):
			changed = true
		case perr != nil:
			return perr
		default:
			hadPrevious = true
			changed = previous.Status != status ||
				previous.CanReceiveInvoice != result.CanReceiveInvoice ||
				previous.CanReceiveCreditNote != result.CanReceiveCreditNote
		}

		if err := txq.UpsertCustomerPeppolLookup(ctx, store.UpsertCustomerPeppolLookupParams{
			CustomerID: customerID, ParticipantID: participant, Status: status,
			CanReceiveInvoice: result.CanReceiveInvoice, CanReceiveCreditNote: result.CanReceiveCreditNote,
			SmpHost: smpHost, CheckedAt: now,
		}); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		var previousStatus *string
		if hadPrevious {
			previousStatus = &previous.Status
		}
		return recordCustomerPeppolLookup(ctx, txq, now, customerID, status, result.CanReceiveInvoice, result.CanReceiveCreditNote, smpHost, previousStatus, act.Kind, act.Display, act.UserID)
	})
	if err != nil {
		return peppolLookupOutcome{}, fmt.Errorf("customers: record peppol lookup: %w", err)
	}
	return peppolLookupOutcome{
		Status: status, CanReceiveInvoice: result.CanReceiveInvoice, CanReceiveCreditNote: result.CanReceiveCreditNote,
		SMPHost: smpHost, CheckedAt: now, Changed: changed,
	}, nil
}
```

The handler and the worker owe different answers for the network failing and for the database failing — a 502 versus a 500, a log line versus giving up — so the two are told apart by a sentinel, not by the shape of an error string. Add beside `peppolLookupUnavailableResponse`:

```go
// errPeppolLookupUnavailable is what lookupAndStorePeppol answers when the
// NETWORK could not answer, as opposed to when the database could not store
// what it said: the handler owes a 502 for the first and a 500 for the second,
// and the worker logs the first and gives up on the second, so the two must be
// told apart by something better than the shape of an error string.
var errPeppolLookupUnavailable = errors.New("customers: peppol network unavailable")
```

wrap the network failure in `lookupAndStorePeppol`'s own `if err != nil` branch as

```go
		return peppolLookupOutcome{}, fmt.Errorf("%w: %w", errPeppolLookupUnavailable, err)
```

(replacing the `return peppolLookupOutcome{}, err` shown in the function above), and reduce the handler's tail — everything from `lookupCtx := ctx` to its final `return` — to

```go
	outcome, err := s.lookupAndStorePeppol(ctx, req.Id, participant, act)
	if errors.Is(err, errPeppolLookupUnavailable) {
		// Already logged by kind inside lookupAndStorePeppol.
		return peppolLookupUnavailableResponse(), nil
	}
	if err != nil {
		return nil, err
	}

	return gen.PostCustomersByIdPeppolLookup200JSONResponse(
		customerPeppolLookupResponse(outcome.Status, outcome.CanReceiveInvoice, outcome.CanReceiveCreditNote,
			participant, showParticipantID, outcome.SMPHost, outcome.CheckedAt),
	), nil
```

Then re-run the handler's tests: `mise exec -- go test -count=1 -run 'TestPostCustomersByIdPeppolLookup|TestBillingProfile' ./internal/customers/`. Same count, all green. If any test changed behaviour, the extraction is wrong — fix the extraction, never the test.

- [ ] **Step 2: Write the candidate queries**

Create `apps/server/internal/customers/queries/peppol_recheck.sql`:

```sql
-- name: AgedPeppolLookups :many
-- AgedPeppolLookups is the re-check worker's first candidate set (registry
-- workers design D6): customers whose stored Peppol answer is older than the
-- re-check age, oldest first.
--
-- The participant equality D6 also requires is NOT here: the participant a
-- customer resolves to today is peppolId or else "0192:"+the organisation
-- number, and only Go knows that rule (lookupParticipant, billing_values.go's
-- derivedPeppolID and its check-digit validation). Duplicating it in SQL would
-- be a second copy free to drift; the worker filters the rows this returns
-- instead, which is why it asks for a batch and may refresh fewer.
SELECT c.id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type,
       c.invoice_email, c.reminder_email, c.payment_terms_days, c.currency, c.language,
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference,
       l.participant_id, l.checked_at
FROM customers.customers c
JOIN customers.customer_peppol_lookups l ON l.customer_id = c.id
WHERE c.status <> 'archived'
  AND l.checked_at < @checked_before::timestamptz
ORDER BY l.checked_at, c.id
LIMIT @row_limit;

-- name: EhfCustomersWithoutPeppolLookup :many
-- EhfCustomersWithoutPeppolLookup is the second candidate set (design D6): a
-- customer whose invoices are already being sent to Peppol and whose
-- registration has never actually been checked. That combination is the one
-- worth a network call unprompted — everyone else's EHF readiness is a
-- question nobody has asked yet, and this worker does not go looking for it.
SELECT c.id, c.type, c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type,
       c.invoice_email, c.reminder_email, c.payment_terms_days, c.currency, c.language,
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference
FROM customers.customers c
LEFT JOIN customers.customer_peppol_lookups l ON l.customer_id = c.id
WHERE l.customer_id IS NULL
  AND c.status <> 'archived'
  AND c.invoice_delivery = 'ehf'
ORDER BY c.id
LIMIT @row_limit;
```

Generate and read what sqlc produced (`AgedPeppolLookupsParams{CheckedBefore time.Time; RowLimit int32}`, `EhfCustomersWithoutPeppolLookup` taking a bare `int32`):

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```

- [ ] **Step 3: Write the failing worker tests**

Create `apps/server/internal/customers/peppol_recheck_worker_test.go`:

```go
package customers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/peppol"
)

// This file is the Peppol re-check worker (registry workers design D5, D6),
// driven through RunCycle against the DB harness with a fake
// Deps.PeppolLookup. No test here resolves a name or opens a socket.

// peppolCheckedAt is the stored lookup's checked_at, or nil when the customer
// has never been checked.
func peppolCheckedAt(t *testing.T, h *modtest.Harness, customerID int32) *time.Time {
	t.Helper()
	return modtest.One[*time.Time](t, h,
		`SELECT checked_at FROM customers.customer_peppol_lookups WHERE customer_id = $1`, customerID)
}

// agePeppolLookup backdates a stored lookup, which is how a test makes a row
// "old" without advancing the harness clock past everything else in the
// installation.
func agePeppolLookup(t *testing.T, h *modtest.Harness, customerID int32, at time.Time) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customer_peppol_lookups SET checked_at = $2 WHERE customer_id = $1`, customerID, at)
}

// TestPeppolRecheckWorker_AsksAgainForAnAgedAnswerAndNotForAFreshOne pins
// design D6's first candidate set and its bound: an answer older than
// CUSTOMERS_PEPPOL_RECHECK_AGE is asked again, and one younger than it is left
// alone. The second half is the one that matters — a worker that re-asked
// everything every cycle would be a daily network call per customer for an
// answer that had not changed.
func TestPeppolRecheckWorker_AsksAgainForAnAgedAnswerAndNotForAFreshOne(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)

	aged := createNorwegianBusiness(t, c, "Aged AS", "923609016")
	fresh := createNorwegianBusiness(t, c, "Fresh AS", "974760673")
	if r := postPeppolLookup(t, c, aged.Id); r.Status != 200 {
		t.Fatalf("seed the aged lookup: %d %s", r.Status, r.Body)
	}
	if r := postPeppolLookup(t, c, fresh.Id); r.Status != 200 {
		t.Fatalf("seed the fresh lookup: %d %s", r.Status, r.Body)
	}
	agePeppolLookup(t, h, aged.Id, h.Now().Add(-800*time.Hour))
	before := len(calls.all())

	if ran, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
	asked := calls.all()[before:]
	if len(asked) != 1 || asked[0] != "0192:923609016" {
		t.Errorf("participants asked = %v, want exactly [0192:923609016]", asked)
	}
	if at := peppolCheckedAt(t, h, aged.Id); at == nil || !at.Equal(h.Now()) {
		t.Errorf("the aged lookup's checked_at = %v, want the cycle's clock %v", at, h.Now())
	}
}

// TestPeppolRecheckWorker_LeavesALookupWhoseParticipantChanged pins design
// D6's participant rule. A lookup made for a participant the customer no
// longer resolves to is stale BY IDENTITY: the billing profile already drops
// it from every response and every warning (resolvedPeppolLookup), so
// refreshing it would spend a network call to update a row nothing reads, and
// re-stamping its checked_at would make it look current while still naming the
// wrong participant.
func TestPeppolRecheckWorker_LeavesALookupWhoseParticipantChanged(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls, peppol.Result{Registered: true}, nil)))
	c := authenticatedClient(t, h)

	created := createNorwegianBusiness(t, c, "Moved AS", "923609016")
	if r := postPeppolLookup(t, c, created.Id); r.Status != 200 {
		t.Fatalf("seed the lookup: %d %s", r.Status, r.Body)
	}
	agePeppolLookup(t, h, created.Id, h.Now().Add(-800*time.Hour))
	// The stored row now names a participant this customer does not resolve to.
	h.Exec(t, `UPDATE customers.customer_peppol_lookups SET participant_id = '0192:974760673' WHERE customer_id = $1`, created.Id)
	stored := peppolCheckedAt(t, h, created.Id)
	before := len(calls.all())

	if _, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if asked := calls.all()[before:]; len(asked) != 0 {
		t.Errorf("participants asked = %v, want none: the stored lookup is already stale by identity", asked)
	}
	if at := peppolCheckedAt(t, h, created.Id); at == nil || stored == nil || !at.Equal(*stored) {
		t.Errorf("checked_at = %v, want it untouched at %v", at, stored)
	}
}

// TestPeppolRecheckWorker_ChecksAnEhfCustomerThatHasNeverBeenChecked pins
// design D6's second candidate set: the customer whose invoices are already
// going to Peppol is the one whose registration must not be assumed.
func TestPeppolRecheckWorker_ChecksAnEhfCustomerThatHasNeverBeenChecked(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)

	created := createNorwegianBusiness(t, c, "Already Sending AS", "923609016")
	h.Exec(t, `UPDATE customers.customers SET invoice_delivery = 'ehf' WHERE id = $1`, created.Id)
	if at := peppolCheckedAt(t, h, created.Id); at != nil {
		t.Fatalf("checked_at = %v before the cycle, want no stored lookup at all", at)
	}

	if _, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if asked := calls.all(); len(asked) != 1 || asked[0] != "0192:923609016" {
		t.Errorf("participants asked = %v, want exactly [0192:923609016]", asked)
	}
	if at := peppolCheckedAt(t, h, created.Id); at == nil {
		t.Error("the ehf customer still has no stored lookup after a cycle")
	}
}

// TestPeppolRecheckWorker_LeavesANonEhfCustomerWithNoLookupAlone is the
// boundary of that second set: "could receive EHF" is a question a person asks
// with a click (design D6's own scope), and a worker that asked it for every
// customer would turn an offer into a crawl of the Peppol network.
func TestPeppolRecheckWorker_LeavesANonEhfCustomerWithNoLookupAlone(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls, peppol.Result{Registered: true}, nil)))
	c := authenticatedClient(t, h)
	createNorwegianBusiness(t, c, "Emailed AS", "923609016")

	if _, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if asked := calls.all(); len(asked) != 0 {
		t.Errorf("participants asked = %v, want none", asked)
	}
}

// TestPeppolRecheckWorker_RecordsAnEventOnlyWhenTheAnswerChanged pins design
// D6's event rule and its actor: a re-check that finds the same answer is
// silent — otherwise every customer would collect a timeline entry a month
// saying nothing happened — and one that finds a different answer is recorded
// with the system actor, not attributed to whoever clicked last.
func TestPeppolRecheckWorker_RecordsAnEventOnlyWhenTheAnswerChanged(t *testing.T) {
	t.Parallel()
	registered := true
	h := newHarness(t, modtest.WithPeppolLookup(func(context.Context, string) (peppol.Result, error) {
		if registered {
			return peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil
		}
		return peppol.Result{Registered: false}, nil
	}))
	c := authenticatedClient(t, h)

	created := createNorwegianBusiness(t, c, "Lapsing AS", "923609016")
	if r := postPeppolLookup(t, c, created.Id); r.Status != 200 {
		t.Fatalf("seed the lookup: %d %s", r.Status, r.Body)
	}
	if n := len(fetchPeppolLookupEvents(t, c, created.Id)); n != 1 {
		t.Fatalf("events after the click = %d, want 1", n)
	}

	w := customers.NewPeppolRecheckWorker(h.Deps())
	agePeppolLookup(t, h, created.Id, h.Now().Add(-800*time.Hour))
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("unchanged RunCycle: %v", err)
	}
	if n := len(fetchPeppolLookupEvents(t, c, created.Id)); n != 1 {
		t.Errorf("events after an unchanged re-check = %d, want still 1", n)
	}

	registered = false
	agePeppolLookup(t, h, created.Id, h.Now().Add(-800*time.Hour))
	if _, err := w.RunCycle(context.Background()); err != nil {
		t.Fatalf("changed RunCycle: %v", err)
	}
	events := fetchPeppolLookupEvents(t, c, created.Id)
	if len(events) != 2 {
		t.Fatalf("events after a changed re-check = %d, want 2", len(events))
	}
	last := events[len(events)-1]
	if last.ActorKind != "system" || str(last.ActorDisplay) != "System" {
		t.Errorf("event actor = %q/%q, want system/System (design D6)", last.ActorKind, str(last.ActorDisplay))
	}
}

// TestPeppolRecheckWorker_AFailureLeavesCheckedAtAlone pins design D6's
// failure rule: a customer whose re-check could not complete stays first in
// line next cycle, which it only does while its checked_at still says how long
// it has been since anyone actually got an answer.
func TestPeppolRecheckWorker_AFailureLeavesCheckedAtAlone(t *testing.T) {
	t.Parallel()
	fail := false
	h := newHarness(t, modtest.WithPeppolLookup(func(context.Context, string) (peppol.Result, error) {
		if fail {
			return peppol.Result{}, errors.New("peppol: simulated network failure")
		}
		return peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil
	}))
	c := authenticatedClient(t, h)

	created := createNorwegianBusiness(t, c, "Unreachable AS", "923609016")
	if r := postPeppolLookup(t, c, created.Id); r.Status != 200 {
		t.Fatalf("seed the lookup: %d %s", r.Status, r.Body)
	}
	aged := h.Now().Add(-800 * time.Hour)
	agePeppolLookup(t, h, created.Id, aged)

	fail = true
	if ran, err := customers.NewPeppolRecheckWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil — one unreachable customer is not a failed cycle", ran, err)
	}
	if at := peppolCheckedAt(t, h, created.Id); at == nil || !at.UTC().Equal(aged.UTC()) {
		t.Errorf("checked_at = %v, want it untouched at %v", at, aged)
	}
}

// TestPeppolRecheckWorker_SkipsTheCycleWhenTheLeaseIsHeld is design D5 through
// this worker: two replicas must not both spend the batch's worth of network
// lookups on the same hundred customers.
func TestPeppolRecheckWorker_SkipsTheCycleWhenTheLeaseIsHeld(t *testing.T) {
	t.Parallel()
	calls := &peppolLookupCalls{}
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(calls, peppol.Result{Registered: true}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Contended AS", "923609016")
	h.Exec(t, `UPDATE customers.customers SET invoice_delivery = 'ehf' WHERE id = $1`, created.Id)

	ctx := context.Background()
	holder, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the lease holder's connection: %v", err)
	}
	defer holder.Release()
	var locked bool
	if err := holder.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, customers.PeppolRecheckLeaseKeyForTest).Scan(&locked); err != nil {
		t.Fatalf("take the lease: %v", err)
	}
	if !locked {
		t.Fatal("the lease was already held; this test's database is its own")
	}

	w := customers.NewPeppolRecheckWorker(h.Deps())
	ran, err := w.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if ran {
		t.Error("RunCycle ran while another holder had the lease")
	}
	if asked := calls.all(); len(asked) != 0 {
		t.Errorf("participants asked = %v while the lease was held, want none", asked)
	}

	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, customers.PeppolRecheckLeaseKeyForTest); err != nil {
		t.Fatalf("release the lease: %v", err)
	}
	ran, err = w.RunCycle(ctx)
	if err != nil {
		t.Fatalf("RunCycle after release: %v", err)
	}
	if !ran {
		t.Fatal("RunCycle still reported the lease held after it was released")
	}
	if n := h.Count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); n != 1 {
		// Exactly one: the holder's own, still taken by this test.
		t.Errorf("advisory locks held = %d, want 1 (the test's own holder)", n)
	}
}

// TestPeppolRecheckWorker_RunStopsWithItsContext pins the loop contract the
// runner depends on.
func TestPeppolRecheckWorker_RunStopsWithItsContext(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithPeppolLookup(stubPeppolLookup(nil,
		peppol.Result{Registered: true, CanReceiveInvoice: true, CanReceiveCreditNote: true}, nil)))
	c := authenticatedClient(t, h)
	created := createNorwegianBusiness(t, c, "Ticking AS", "923609016")
	h.Exec(t, `UPDATE customers.customers SET invoice_delivery = 'ehf' WHERE id = $1`, created.Id)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- customers.NewPeppolRecheckWorker(h.Deps()).Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for peppolCheckedAt(t, h, created.Id) == nil {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("Run never checked the ehf customer")
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

// TestPeppolRecheckWorker_IsNotStartedWithoutTheLookupItself pins design D6's
// dependency: "effective only with PEPPOL_LOOKUP_ENABLED=1" has to mean the
// worker is never handed to the runner, not that it starts and finds a nil
// seam.
func TestPeppolRecheckWorker_IsNotStartedWithoutTheLookupItself(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("PEPPOL_LOOKUP_ENABLED", "0"))
	for _, w := range module.Workers(h.Deps(), customers.Module()) {
		if w.Name() == "customers-peppol-recheck" {
			t.Error("the re-check worker was started with PEPPOL_LOOKUP_ENABLED=0")
		}
	}
}
```
(the last test needs `"github.com/vantigo-io/vantigo/server/internal/module"` imported in this file.)

Update the exact set in `module_test.go`:

```go
	want := []string{"customers-peppol-recheck", "customers-registry-feed"}
```

and add one case there for the second switch:

```go
// TestModule_ThePeppolRecheckWorkerCanBeTurnedOff is design D7's second
// switch, checked the same way the first one is: off means never started.
func TestModule_ThePeppolRecheckWorkerCanBeTurnedOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t, modtest.WithEnv("CUSTOMERS_PEPPOL_RECHECK_ENABLED", "0"))
	var names []string
	for _, w := range module.Workers(h.Deps(), customers.Module()) {
		names = append(names, w.Name())
	}
	if !slices.Equal(names, []string{"customers-registry-feed"}) {
		t.Errorf("workers = %v, want only the feed worker", names)
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestPeppolRecheckWorker|TestModule_' ./internal/customers/
```
Expected: `undefined: customers.NewPeppolRecheckWorker`, `undefined: customers.PeppolRecheckLeaseKeyForTest`.

- [ ] **Step 5: Write `peppol_recheck_worker.go`**

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the Peppol re-check worker (registry workers design D5, D6): it
// asks the Peppol network, on a schedule, the question a person's click asks
// today.
//
// What it deliberately does NOT do is decide anything. A lapsed registration
// surfaces through the billing profile's existing warnings
// (ehf_recipient_not_registered, ehf_available), which already derive from the
// stored lookup — so this worker needs no new code to be visible and adds no
// attention item of its own. It never switches a customer's invoiceDelivery
// either, in either direction: phase 2 delivery B's decision 2 stands, and only
// a person changes where a customer's invoices go.
//
// The two candidate sets are not symmetrical, and the asymmetry is the design:
//
//   - an aged stored answer, whose participant is still the one the customer
//     resolves to today. A lookup for a participant that changed is already
//     stale by identity — the billing profile drops it from every response and
//     every warning — so refreshing it would update a row nothing reads, and
//     re-stamping its checked_at would make it look current while naming the
//     wrong participant;
//   - a customer whose invoice_delivery is already 'ehf' and who has NO stored
//     answer at all. "Could this customer receive EHF" is a question a person
//     asks with a click; "is the customer we are already sending EHF to
//     actually registered" is one nobody should have to ask.

const (
	// peppolRecheckWorkerName is what the runner logs this worker as, in the
	// same <module>-<worker> spelling as the feed worker beside it.
	peppolRecheckWorkerName = "customers-peppol-recheck"

	// peppolRecheckLeaseKey is the ASCII string "CUSTPEP1" read as a big-endian
	// 64-bit value (design D5) — its own key, not the feed worker's: the two do
	// unrelated work and one holding the other's lease would silently halve
	// both.
	peppolRecheckLeaseKey int64 = 0x4355535450455031

	// peppolRecheckBatch bounds one cycle at a hundred customers (design D6).
	// Each one is a DNS query and possibly an SMP request against a public
	// network, one at a time, so the batch is what keeps a large installation's
	// nightly cycle from looking like a crawl of it.
	peppolRecheckBatch = 100

	// The defaults a worker built from a Deps with no Config falls back on — the
	// same values config.go defaults CUSTOMERS_PEPPOL_RECHECK_POLL and
	// CUSTOMERS_PEPPOL_RECHECK_AGE to.
	defaultPeppolRecheckPoll = 24 * time.Hour
	defaultPeppolRecheckAge  = 720 * time.Hour
)

// PeppolRecheckWorker re-asks the Peppol network about customers whose stored
// answer has aged, and about customers already set to receive EHF who have no
// stored answer at all. It implements worker.Worker.
type PeppolRecheckWorker struct {
	deps module.Deps
	srv  *server
}

var _ worker.Worker = (*PeppolRecheckWorker)(nil)

// NewPeppolRecheckWorker builds the worker over d. s.peppolLookup is nil
// whenever PEPPOL_LOOKUP_ENABLED is off (newServer's own rule), and the module
// does not register this worker in that case — so a cycle never has to wonder
// whether it has a network to ask.
func NewPeppolRecheckWorker(d module.Deps) *PeppolRecheckWorker {
	return &PeppolRecheckWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *PeppolRecheckWorker) Name() string { return peppolRecheckWorkerName }

// Interval is the poll cadence between cycles (CUSTOMERS_PEPPOL_RECHECK_POLL,
// default 24 hours).
func (w *PeppolRecheckWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersPeppolRecheckPoll > 0 {
		return w.deps.Config.CustomersPeppolRecheckPoll
	}
	return defaultPeppolRecheckPoll
}

// Run is the worker loop, the same shape the feed worker's is.
func (w *PeppolRecheckWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			w.logger().Error("peppol re-check cycle failed", "worker", peppolRecheckWorkerName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the advisory lease, reporting whether it ran:
// the never-checked EHF customers first, then the aged answers, up to
// peppolRecheckBatch between them.
//
// The EHF customers come first because they are the ones whose invoices are
// already being sent somewhere nobody has verified: on an installation with
// more aged answers than the batch, the customer that matters most must not be
// the one that never fits.
func (w *PeppolRecheckWorker) RunCycle(ctx context.Context) (bool, error) {
	return w.underLease(ctx, func(ctx context.Context) error {
		if w.srv.peppolLookup == nil {
			// Unreachable through module.Workers, which does not register this
			// worker without the lookup — but a worker constructed directly
			// answers "nothing to do" rather than panicking on a nil seam.
			return nil
		}
		q := store.New(w.deps.Pool)

		missing, err := q.EhfCustomersWithoutPeppolLookup(ctx, peppolRecheckBatch)
		if err != nil {
			return fmt.Errorf("customers: select ehf customers without a peppol lookup: %w", err)
		}
		checked, failed := 0, 0
		for _, row := range missing {
			if ctx.Err() != nil {
				return nil
			}
			profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
				row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference)
			identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)
			participant, _ := lookupParticipant(profile, identity, row.Type)
			if participant == "" {
				// invoiceDelivery says ehf but nothing says who to: the billing
				// profile's own ehf_without_recipient warning already reports that,
				// and there is nothing to ask the network about.
				continue
			}
			if w.recheck(ctx, row.ID, participant) {
				checked++
			} else {
				failed++
			}
		}

		remaining := peppolRecheckBatch - len(missing)
		if remaining > 0 {
			aged, err := q.AgedPeppolLookups(ctx, store.AgedPeppolLookupsParams{
				CheckedBefore: w.now().Add(-w.recheckAge()), RowLimit: int32(remaining),
			})
			if err != nil {
				return fmt.Errorf("customers: select aged peppol lookups: %w", err)
			}
			for _, row := range aged {
				if ctx.Err() != nil {
					return nil
				}
				profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
					row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference)
				identity := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType)
				participant, _ := lookupParticipant(profile, identity, row.Type)
				// The participant rule (design D6, this file's header): only a
				// lookup that is still about the participant this customer
				// resolves to today is this worker's to refresh.
				if participant == "" || participant != row.ParticipantID {
					continue
				}
				if w.recheck(ctx, row.ID, participant) {
					checked++
				} else {
					failed++
				}
			}
		}
		w.logger().Info("peppol re-check cycle finished", "worker", peppolRecheckWorkerName,
			"checked", checked, "failed", failed)
		return nil
	})
}

// underLease is design D5's lease, the same shape the feed worker's and
// communications/retention.go's are (see either for why the unlock runs on a
// context stripped of cancellation and why a failed unlock discards the
// connection), on this worker's own key.
func (w *PeppolRecheckWorker) underLease(ctx context.Context, action func(context.Context) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("customers: acquire a connection for the peppol re-check lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, peppolRecheckLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("customers: take the peppol re-check lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the customers peppol re-check lease is held by another replica; skipping this cycle",
			"worker", peppolRecheckWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, peppolRecheckLeaseKey); err != nil {
			w.logger().Error("releasing the peppol re-check lease failed; discarding the connection",
				"worker", peppolRecheckWorkerName, "error", err)
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx)
}

// recheck is one customer asked again through the click's own function, with
// the system actor, reporting whether it succeeded. A network failure is
// already logged by kind inside lookupAndStorePeppol and leaves checked_at
// alone, so the customer is first in line next cycle (design D6); a database
// failure is logged here, because a cycle that stopped at the first one would
// leave the rest of the batch unchecked for a whole poll interval over a
// problem that may be one row's.
func (w *PeppolRecheckWorker) recheck(ctx context.Context, customerID int32, participant string) bool {
	if _, err := w.srv.lookupAndStorePeppol(ctx, customerID, participant, generatedFallbackActor); err != nil {
		if !errors.Is(err, errPeppolLookupUnavailable) {
			w.logger().Error("customers: storing a peppol re-check failed",
				"worker", peppolRecheckWorkerName, "customerId", customerID, "error", err.Error())
		}
		return false
	}
	return true
}

// recheckAge is how old a stored answer must be before it is asked again
// (CUSTOMERS_PEPPOL_RECHECK_AGE, default 720 hours).
func (w *PeppolRecheckWorker) recheckAge() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersPeppolRecheckAge > 0 {
		return w.deps.Config.CustomersPeppolRecheckAge
	}
	return defaultPeppolRecheckAge
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers.
func (w *PeppolRecheckWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *PeppolRecheckWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
```

Register it in `module.go`'s `workers`, after the feed worker:

```go
	if d.Config.PeppolLookupEnabled && d.Config.CustomersPeppolRecheckEnabled {
		out = append(out, NewPeppolRecheckWorker(d))
	}
```
and extend that function's doc comment's first sentence to "the Brreg update-feed worker and the Peppol re-check worker".

Add to `export_test.go`:

```go
// PeppolRecheckLeaseKeyForTest is this worker's advisory-lease key, exported so
// a test can take the same lock from a second connection and prove a cycle
// skips (design D5).
const PeppolRecheckLeaseKeyForTest = peppolRecheckLeaseKey
```

- [ ] **Step 6: Run the tests to verify they pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 ./internal/customers/ ./internal/module/
```
Expected: PASS, the Peppol handler's own tests included and unchanged.

- [ ] **Step 7: Prove the tests can fail**

1. Drop the `participant != row.ParticipantID` term — `TestPeppolRecheckWorker_LeavesALookupWhoseParticipantChanged` goes red.
2. Change `EhfCustomersWithoutPeppolLookup`'s `c.invoice_delivery = 'ehf'` to `TRUE` — `TestPeppolRecheckWorker_LeavesANonEhfCustomerWithNoLookupAlone` goes red.
3. In `lookupAndStorePeppol`, make `changed` always true — `TestPeppolRecheckWorker_RecordsAnEventOnlyWhenTheAnswerChanged` goes red on the unchanged half.
4. In `recheck`, upsert `checked_at` before returning on a failure (or simply drop the early return in `lookupAndStorePeppol`) — `TestPeppolRecheckWorker_AFailureLeavesCheckedAtAlone` goes red.

- [ ] **Step 8: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go vet ./... && mise exec -- golangci-lint run ./internal/customers/...
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(customers): the Peppol network is asked again on a schedule, and the click shares the answer path' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-task4
git add apps/server/internal/customers/queries/peppol_recheck.sql apps/server/internal/customers/store apps/server/internal/customers/peppol_lookup.go apps/server/internal/customers/peppol_recheck_worker.go apps/server/internal/customers/peppol_recheck_worker_test.go apps/server/internal/customers/module.go apps/server/internal/customers/module_test.go apps/server/internal/customers/export_test.go
git commit -F /tmp/msg-task4 -- apps/server/internal/customers/queries/peppol_recheck.sql apps/server/internal/customers/store apps/server/internal/customers/peppol_lookup.go apps/server/internal/customers/peppol_recheck_worker.go apps/server/internal/customers/peppol_recheck_worker_test.go apps/server/internal/customers/module.go apps/server/internal/customers/module_test.go apps/server/internal/customers/export_test.go
git show --stat HEAD && git status --short
```

---

### Task 5: The hint on the wire and on the card (D2)

**Files:**
- Modify: `openapi/customers.yaml` (`CustomerRegistryRecord` gains `registryUpdatedHint`; `CustomerRegistryAddress.countryCode` leaves `required`), `apps/server/internal/customers/registry.go`, `apps/server/internal/customers/registry_test.go`, `apps/customers/frontend/src/api/registry.ts` (+`registry.test.ts`), `apps/customers/frontend/src/pages/-customer-registry-card.tsx` (+`-customer-registry-card.test.tsx`), `apps/customers/frontend/src/i18n.ts`
- Generated: `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, the changed `api-schema.d.ts` files, `openapi/COVERAGE.md` if it moves
- Read first (do not change): `openapi/customers.yaml:396-501`, `registry.go:111-119` (`registryRecord`), `:271-307` (`registryRecordFromRow`), `:347-393` (the two response builders), `:674-694` (where `after` is built)

**Interfaces:**
- Produces: `CustomerRegistryRecord.registryUpdatedHint?: string` (`date-time`, omitted while NULL); `CustomerRegistryAddress.countryCode` optional; Go `registryRecord.RegistryUpdatedHint *time.Time`; TS `CustomerRegistryRecord.registryUpdatedHint: string | null` and `CustomerRegistryAddress.countryCode: string` (absent normalised to `""`).

- [ ] **Step 1: Change the contract**

In `openapi/customers.yaml`, inside `CustomerRegistryRecord.properties` (the properties are in alphabetical order — this one goes between `postalAddress` and `underForcedLiquidation`):

```yaml
                registryUpdatedHint:
                    format: date-time
                    nullable: true
                    type: string
```

Extend that schema's `description` with one sentence, appended inside the existing quoted string:

> ` registryUpdatedHint is the moment Brønnøysundregistrene's own update feed said this entity changed, written by the background feed worker before it re-reads the record: while it is newer than fetchedAt the record is known to be behind the register — a refresh that failed, or one not attempted yet — and the card says so. It is absent whenever the feed has never reported a change for this customer.`

In `CustomerRegistryAddress`, change

```yaml
            required:
                - lines
                - countryCode
```
to
```yaml
            required:
                - lines
```
and amend that schema's `description`, replacing `countryCode is ISO 3166-1 alpha-2, empty only when the registry sent none.` with `countryCode is ISO 3166-1 alpha-2, and absent when the registry sent none — the registry only assigns a country code to some foreign addresses, and an empty string was never a country.`

Then generate:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && mise exec -- go test -count=1 ./internal/openapi/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && git status --short
```
`gen.CustomerRegistryAddress.CountryCode` becomes `*string` and `gen.CustomerRegistryRecord` gains `RegistryUpdatedHint *time.Time` — the Go build now fails in `registry.go`, which Step 3 fixes. No operation was added, so `COVERAGE.md` should not move; commit it only if it does.

- [ ] **Step 2: Write the failing Go test**

Add to `apps/server/internal/customers/registry_test.go`'s `registryRecordJSON` the field

```go
	RegistryUpdatedHint      *time.Time           `json:"registryUpdatedHint"`
```
and change `registryAddressJSON.CountryCode` to `*string` (it is now omitted rather than sent as `""`), updating the existing assertions that read it — `str(rec.BusinessAddress.CountryCode)` in place of `rec.BusinessAddress.CountryCode`. Then add:

```go
// TestRegistryRecord_ReportsTheFeedsHint pins design D2's one visible
// consequence on the wire: the hint the feed worker wrote is readable, so the
// card can say the record is behind the register. A record the feed has never
// reported on omits the field entirely rather than sending null (this module's
// wire rule).
func TestRegistryRecord_ReportsTheFeedsHint(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registryBody(equinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

	if rec := fetchRegistryRecord(t, c, created.Id); rec.RegistryUpdatedHint != nil {
		t.Errorf("registryUpdatedHint = %v before any feed entry, want it absent", rec.RegistryUpdatedHint)
	}
	if body := getRegistryRecord(t, c, created.Id).Body; strings.Contains(body, "registryUpdatedHint") {
		t.Errorf("body = %s, want no registryUpdatedHint key at all", body)
	}

	hint := h.Now().Add(time.Hour).UTC()
	h.Exec(t, `UPDATE customers.customer_registry_records SET registry_updated_hint = $2 WHERE customer_id = $1`,
		created.Id, hint)

	rec := fetchRegistryRecord(t, c, created.Id)
	if rec.RegistryUpdatedHint == nil || !rec.RegistryUpdatedHint.UTC().Equal(hint) {
		t.Errorf("registryUpdatedHint = %v, want %v", rec.RegistryUpdatedHint, hint)
	}
}

// TestRegistryRefresh_KeepsTheHintOnTheRecordItAnswersWith pins the one place
// the two halves of this delivery meet in a response: the upsert deliberately
// never writes registry_updated_hint (the feed worker owns that column), so a
// refresh that answered with a record carrying no hint would be describing a
// row that still has one — the card would then stop showing a line the very
// next GET puts back.
func TestRegistryRefresh_KeepsTheHintOnTheRecordItAnswersWith(t *testing.T) {
	t.Parallel()
	h := newRegistryHarness(t, registrySequence(equinorRegistryBody, movedEquinorRegistryBody))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

	hint := h.Now().Add(time.Hour).UTC()
	h.Exec(t, `UPDATE customers.customer_registry_records SET registry_updated_hint = $2 WHERE customer_id = $1`,
		created.Id, hint)
	// Past the click throttle, so the refresh actually fetches.
	h.Advance(2 * time.Minute)

	r := postRegistryRefresh(t, c, created.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("refresh: status %d body %s, want 200", r.Status, r.Body)
	}
	var result registryRefreshJSON
	r.JSON(&result)
	if result.Record == nil || result.Record.RegistryUpdatedHint == nil || !result.Record.RegistryUpdatedHint.UTC().Equal(hint) {
		t.Errorf("refresh record's hint = %v, want %v carried over from the stored row", result.Record, hint)
	}
	if got := modtest.One[*time.Time](t, h,
		`SELECT registry_updated_hint FROM customers.customer_registry_records WHERE customer_id = $1`, created.Id); got == nil || !got.UTC().Equal(hint) {
		t.Errorf("stored hint = %v after a refresh, want it untouched at %v", got, hint)
	}
}

// TestRegistryRecord_OmitsAnAbsentCountryCode pins the contract relaxation:
// the registry does not always send a landkode, and "" was never a country.
// The field is now absent for such an address rather than an empty string.
func TestRegistryRecord_OmitsAnAbsentCountryCode(t *testing.T) {
	t.Parallel()
	const noCountryCodeBody = `{
		"organisasjonsnummer": "923609016",
		"navn": "EQUINOR ASA",
		"konkurs": false,
		"underAvvikling": false,
		"underTvangsavviklingEllerTvangsopplosning": false,
		"registrertIMvaregisteret": false,
		"forretningsadresse": {"poststed": "81-336 GDYNIA", "adresse": ["ul. Budowniczych 12"]}
	}`
	h := newRegistryHarness(t, registryBody(noCountryCodeBody))
	c := authenticatedClient(t, h)
	created := createBrregPick(t, c, "EQUINOR ASA", "923609016")

	rec := fetchRegistryRecord(t, c, created.Id)
	if rec.BusinessAddress == nil || rec.BusinessAddress.CountryCode != nil {
		t.Errorf("businessAddress = %+v, want one with no countryCode", rec.BusinessAddress)
	}
	if str(rec.BusinessAddress.City) != "81-336 GDYNIA" {
		t.Errorf("city = %q, want 81-336 GDYNIA", str(rec.BusinessAddress.City))
	}
}
```

- [ ] **Step 3: Run the Go tests to verify they fail, then make them pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestRegistryRecord_|TestRegistryRefresh_' ./internal/customers/
```
Expected: a build failure from Step 1's generated change, then failures on the three new cases.

Four edits in `apps/server/internal/customers/registry.go`:

1. `registryRecord` gains the field, replacing that type's paragraph about the column being absent:

```go
	// RegistryUpdatedHint is what the feed worker wrote: the moment Brreg's own
	// update feed said this entity changed (registry workers design D2). It is
	// READ here and sent on the wire, and never written by this file — the
	// upsert leaves the column alone on purpose, so that a refresh can neither
	// invent a hint nor erase the one the worker left. While it is newer than
	// FetchedAt the record is known to be behind the register, which is the
	// single definition of stale the worker's own sweep retries on and the card's
	// one line reports.
	//
	// It is deliberately not diffed (registry_diff.go): "the registry says
	// something changed" is not itself a change to the record, and reporting it
	// as one would put an entry on the timeline every time the feed mentioned
	// the company, next to the entry saying what actually differed.
	RegistryUpdatedHint *time.Time
```

2. `registryRecordFromRow` gains `RegistryUpdatedHint: row.RegistryUpdatedHint,` (the column is already selected by both registry reads and sqlc gives it as `*time.Time`).

3. `registryRecordResponse` gains `RegistryUpdatedHint: rec.RegistryUpdatedHint,`, and `registryAddressResponse`'s `CountryCode: a.CountryCode` becomes `CountryCode: registryOptional(a.CountryCode)` — with that function's comment losing its "except countryCode, which the contract makes required" clause, now that it does not.

4. In `refreshRegistryRecord`, immediately after the `if outcome == brregEntityDeleted { … }` block and before `changes := diffRegistryRecords(...)`:

```go
		// The hint is the feed worker's column, and the upsert below leaves it
		// alone — so the record this call ANSWERS with has to carry it over from
		// the row on file, or a refresh would describe a record without a hint
		// while the stored row still has one, and the card's line would vanish
		// until the next GET put it back. (deletedRegistryRecordFrom already
		// copies it with everything else; this makes the found case agree.)
		if before != nil {
			after.RegistryUpdatedHint = before.RegistryUpdatedHint
		}
```

Re-run: `mise exec -- go test -count=1 ./internal/customers/ ./internal/openapi/`. Expected PASS.

- [ ] **Step 4: Write the failing frontend tests**

In `apps/customers/frontend/src/api/registry.test.ts`, add to the existing describe blocks:

```ts
  it("carries the feed's hint through, and reports its absence as null", async () => {
    stubFetch(vi.fn(() => Promise.resolve(jsonResponse(200, { ...fullRecordBody, registryUpdatedHint: "2026-09-22T11:00:00Z" }))));
    const withHint = await customerRegistryRecordQueryOptions(1001).queryFn!({} as never);
    expect(withHint?.registryUpdatedHint).toBe("2026-09-22T11:00:00Z");

    stubFetch(vi.fn(() => Promise.resolve(jsonResponse(200, minimalRecordBody))));
    const without = await customerRegistryRecordQueryOptions(1001).queryFn!({} as never);
    expect(without?.registryUpdatedHint).toBeNull();
  });

  it("reads an address with no countryCode as an empty one, not as undefined", async () => {
    stubFetch(
      vi.fn(() =>
        Promise.resolve(
          jsonResponse(200, {
            ...minimalRecordBody,
            businessAddress: { lines: ["ul. Budowniczych 12"], city: "81-336 GDYNIA" },
          }),
        ),
      ),
    );
    const record = await customerRegistryRecordQueryOptions(1001).queryFn!({} as never);
    expect(record?.businessAddress).toEqual({
      lines: ["ul. Budowniczych 12"],
      postalCode: null,
      city: "81-336 GDYNIA",
      municipality: null,
      countryCode: "",
    });
  });
```
(match the file's existing way of invoking `queryFn` — read the neighbouring cases and copy it rather than the `{} as never` shorthand above if they do it differently.)

In `apps/customers/frontend/src/pages/-customer-registry-card.test.tsx`:

```tsx
  it("says the register reported a change the record does not have yet", async () => {
    renderCard(
      registryFetch({ ...fullRecordBody, registryUpdatedHint: "2026-09-23T08:00:00Z" }),
    );
    expect(await screen.findByText(/^The registry reported a change on /)).toBeInTheDocument();
  });

  it("says nothing when the record is at least as new as the register's report", async () => {
    // fetchedAt is 2026-09-22T09:00:00Z: a refresh has already caught up.
    renderCard(registryFetch({ ...fullRecordBody, registryUpdatedHint: "2026-09-22T08:00:00Z" }));
    expect(await screen.findByText(/^From Brønnøysundregistrene, fetched /)).toBeInTheDocument();
    expect(screen.queryByText(/^The registry reported a change on /)).not.toBeInTheDocument();
  });

  it("says nothing when the register has reported nothing", async () => {
    renderCard(registryFetch(fullRecordBody));
    expect(await screen.findByText(/^From Brønnøysundregistrene, fetched /)).toBeInTheDocument();
    expect(screen.queryByText(/^The registry reported a change on /)).not.toBeInTheDocument();
  });

  it("renders a foreign address the register sent no country code for, exactly as before", async () => {
    renderCard(
      registryFetch({
        ...minimalRecordBody,
        businessAddress: { lines: ["ul. Budowniczych 12"], city: "81-336 GDYNIA" },
      }),
    );
    expect(await screen.findByText("ul. Budowniczych 12")).toBeInTheDocument();
    expect(screen.getByText("81-336 GDYNIA")).toBeInTheDocument();
  });
```
The last one is the spec's "an absent `countryCode` renders as before": read `-customer-registry-fields.tsx` and `lib/registry-address.ts` first and assert on whatever those two actually render for a city with no postal code — the two expectations above are what the existing tests' `"8900 BRØNNØYSUND"` assertion implies for an address with no postal code, but confirm it rather than assume it.

Run them: `cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/customers/frontend test`. Expected: the three card cases fail (no such text) and the two api cases fail on `registryUpdatedHint` not existing / `countryCode` being `undefined`.

- [ ] **Step 5: Make the frontend tests pass**

`apps/customers/frontend/src/api/registry.ts`:

```ts
export interface CustomerRegistryAddress {
  lines: string[];
  postalCode: string | null;
  city: string | null;
  municipality: string | null;
  /** "" when the registry sent no country code — the field is absent on the wire, and the registry only assigns one to some foreign addresses. */
  countryCode: string;
}
```
(the interface itself is unchanged apart from that comment), plus on the record:

```ts
  /**
   * When Brønnøysundregistrene's own update feed said this entity changed,
   * written by the background feed worker before it re-reads the record. Newer
   * than `fetchedAt` means the record is known to be behind the register — a
   * refresh that failed, or one not attempted yet — which is the one line the
   * card adds for it. Null whenever the feed has never reported a change.
   */
  registryUpdatedHint: string | null;
```

The raw types: `RawCustomerRegistryAddress` becomes `Pick<CustomerRegistryAddress, "lines"> & Partial<Pick<CustomerRegistryAddress, "postalCode" | "city" | "municipality" | "countryCode">>`, and `registryUpdatedHint` needs no change to `RawCustomerRegistryRecord` (it is already covered by the `Partial<Omit<…>>`). The two normalisers:

```ts
        countryCode: raw.countryCode ?? "",
```
```ts
  registryUpdatedHint: raw.registryUpdatedHint ?? null,
```

`apps/customers/frontend/src/pages/-customer-registry-card.tsx` — inside the `record ? (` branch's `<Stack gap="xs">`, directly above the existing `registryFetchedFrom` line:

```tsx
            {/* The register reported a change this record does not have yet
                (design D2): either a background refresh failed, or the feed has
                only just said so and the sweep has not caught up. Not dimmed,
                unlike the provenance line below it — it is the one thing on this
                card that is actionable, and Refresh is right there in the
                header. Both values are instants, so both format in local time. */}
            {isBehindTheRegistry(record) && (
              <Text size="sm">
                {t("registryUpdatedHintLine", {
                  reported: formatters.formatDate(record.registryUpdatedHint as string, {
                    dateStyle: "medium",
                    timeStyle: "short",
                  }),
                  fetched: formatters.formatDate(record.fetchedAt, { dateStyle: "medium", timeStyle: "short" }),
                })}
              </Text>
            )}
```

and, beside `statusBadges` at the bottom of the file:

```tsx
/**
 * Whether the register has reported a change this record does not have yet
 * (design D2): the hint is written before a refresh is attempted, so a hint
 * newer than `fetchedAt` is exactly "the last refresh did not catch up".
 * Compared as instants, not as strings — the two values come from different
 * writes and need not share a format.
 */
const isBehindTheRegistry = (record: CustomerRegistryRecord) =>
  record.registryUpdatedHint !== null &&
  new Date(record.registryUpdatedHint).getTime() > new Date(record.fetchedAt).getTime();
```

`apps/customers/frontend/src/i18n.ts`, beside the other `registry*` keys in **both** catalogs:

```ts
  registryUpdatedHintLine: "The registry reported a change on {{reported}}; this record is from {{fetched}}.",
```
```ts
  registryUpdatedHintLine: "Registeret meldte en endring {{reported}}; disse opplysningene er fra {{fetched}}.",
```

- [ ] **Step 6: Run every frontend check**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/customers/frontend typecheck && mise exec -- bun run --cwd apps/customers/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
```
Expected: PASS throughout.

- [ ] **Step 7: Prove the tests can fail**

Change `>` to `>=` in `isBehindTheRegistry`'s comparison and confirm "says nothing when the record is at least as new" goes red; restore. Drop the `if before != nil { after.RegistryUpdatedHint = … }` carry-over and confirm `TestRegistryRefresh_KeepsTheHintOnTheRecordItAnswersWith` goes red; restore. Change `raw.countryCode ?? ""` to `raw.countryCode` and confirm the api test goes red; restore.

- [ ] **Step 8: Commit**

Two commits, because the contract and the UI are two reviewable things:

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(contracts): a registry record says when the register last reported a change' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-task5a
git add openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go apps/server/internal/customers/registry.go apps/server/internal/customers/registry_test.go
git commit -F /tmp/msg-task5a -- openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go apps/server/internal/customers/registry.go apps/server/internal/customers/registry_test.go

printf '%s\n\n%s\n' 'feat(customers-ui): the Registry card says when the record is behind the register' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-task5b
git add apps/customers/frontend/src/api/registry.ts apps/customers/frontend/src/api/registry.test.ts apps/customers/frontend/src/pages/-customer-registry-card.tsx apps/customers/frontend/src/pages/-customer-registry-card.test.tsx apps/customers/frontend/src/i18n.ts
# plus every api-schema.d.ts gen:client changed (git status --short names them) and openapi/COVERAGE.md only if it moved
git commit -F /tmp/msg-task5b -- apps/customers/frontend/src/api/registry.ts apps/customers/frontend/src/api/registry.test.ts apps/customers/frontend/src/pages/-customer-registry-card.tsx apps/customers/frontend/src/pages/-customer-registry-card.test.tsx apps/customers/frontend/src/i18n.ts
git show --stat HEAD && git status --short
```
Nothing of ours may be left in `git status --short` after the second commit: if a generated `api-schema.d.ts` is still there, add it to the second commit's pathspec and amend.

---

### Task 6: Documentation

**Files:**
- Modify: `docs/customers.md` (a new `### Registry workers` section, the Peppol configuration table, the "What comes next" paragraph), `ROADMAP.md` (the Customers phase 3 entry), `CONTRIBUTING.md` (the worker list and the advisory-lease paragraph)

No code, no tests. Everything below is the text to write; adapt only where the surrounding prose has moved.

- [ ] **Step 1: `docs/customers.md` — the new section**

Insert between the end of `### The frontend` (the registry record's own frontend subsection, ending "…and only for the one address they chose.") and `## Peppol lookup`:

```markdown
### Registry workers

Phase 3 delivery B ("Registry workers",
[design](superpowers/specs/2026-09-22-customers-registry-workers-design.md)): the
same fetch-and-store above, on a schedule instead of a click. Nothing new is shown
to a user beyond one line on the Registry card; what changes is that the record, the
timeline, the attention list and the billing warnings stop going stale.

Two workers, registered through the module's `Workers` field, so they run wherever
this deployment already runs workers: in `api` mode with `WORKERS_IN_PROCESS=1`, and
in `worker` mode — **never** in `server` mode. Each takes its own session-scoped
`pg_try_advisory_lock` (`"CUSTREG1"` and `"CUSTPEP1"` read as 64-bit values), so
several replicas are safe: one runs the cycle, the others log at debug level and skip
it.

**`customers-registry-feed`** (`CUSTOMERS_REGISTRY_FEED_POLL`, default 15 minutes)
reads `GET /enhetsregisteret/api/oppdateringer/enheter` — Brreg's incremental update
feed, which says *which* entities changed, never what — from a stored cursor, and
re-reads each matched entity whole through the Refresh path above.

One cycle, in order:

1. **The sweep.** Up to **50** records whose `registry_updated_hint` is newer than
   their `fetched_at` (oldest hint first), then up to **25** non-archived Norwegian
   business customers with a valid organisation number and **no record at all**
   (lowest id first). The first half is the retry mechanism; the second is the
   backfill — customers created before delivery A, and picks whose fetch failed, get
   their record without anyone clicking, a few hundred an hour, so a large
   installation is caught up within a day. A backfilled record goes through the
   ordinary first-fetch diff, so a hand-typed name that differs from the registry's
   raises `registryRenamed` exactly as a click would. A sweep refresh that fails is
   logged and left for the next cycle, and a sweep never touches the cursor.
2. **The feed**, in pages of 1000, at most **20** pages per cycle (so a week's
   backlog — about 21 000 entries — clears in two cycles), each response capped at
   4 MiB. With no stored cursor the first request is `?dato=<started_at>`: the feed
   is joined at the moment the worker first ran, never at the beginning of time, and
   what came before is the sweep's business. Afterwards it is
   `?oppdateringsid=<next_update_id>`.
3. **Per page**: the page's organisation numbers are matched against this
   installation's non-archived Norwegian business customers **locally** — the
   `organisasjonsnummer` filter is deliberately not used, because chunked filtered
   requests have no safe cursor, while the unfiltered scan's cursor is exact. Every
   `endringstype` counts (`Ny`, `Endring`, `Sletting`, `Fjernet` and the older
   `Ukjent` alike): the entity is re-read whole whatever the reason. Per matched
   customer, once even when the page names it several times: write
   `registry_updated_hint` (the newest of its entries, never moving backwards), then
   refresh. The cursor is written only after the whole page is handled, as the
   page's highest id **plus one** — `oppdateringsid` is inclusive.

A feed request that fails ends the cycle with the cursor untouched, so the next
cycle re-reads the same page. A *refresh* that fails does not: the hint records that
the register has something newer, `hint > fetched_at` is the definition of stale, and
the next cycle's sweep retries it. Every refresh is attributed to the system actor
(`System`) with `producer: customers.brreg`, and the four outcomes keep their
meaning — `Fjernet` in the feed becomes a 410 from the entity endpoint and the row is
deleted with one event; `Sletting` becomes a `SlettetEnhet` body; `unknown` stores
nothing. The 60-second click throttle is the HTTP handler's and does not apply: the
worker only asks when the feed or the sweep says there is a reason.

The cursor lives on `customers.registry_feed_cursor` (migration `00023`), one row:
`next_update_id`, `started_at`, `last_polled_at` and `last_update_at` (the `dato` of
the last entry processed). Nothing reads the last two — they are there because the
only report this delivery gives an operator is a log line, and those two columns
answer "is it running" and "how far behind is it" from `psql` alone. A cycle's
outcome is one log line per page (entries seen, matched, refreshed, the new cursor)
plus one for the sweep.

**`customers-peppol-recheck`** (`CUSTOMERS_PEPPOL_RECHECK_POLL`, default 24 hours,
effective only with `PEPPOL_LOOKUP_ENABLED=1`) asks the Peppol network again, for up
to **100** customers a cycle:

- customers whose `invoice_delivery` is `ehf` and who have **no stored lookup at
  all** — the customer whose invoices are already going to Peppol is the one whose
  registration must not be assumed. These come first, so on an installation with
  more aged answers than the batch they are never the ones that do not fit;
- then non-archived customers with a stored lookup older than
  `CUSTOMERS_PEPPOL_RECHECK_AGE` (default 720h / 30 days), oldest first, **whose
  `participant_id` still equals the participant the billing profile resolves to
  today**. A lookup for a participant that changed is already stale by identity —
  [the billing profile](#billing-profile) drops it from every response and every
  warning — so it is not this worker's to refresh.

Each is the handler's own lookup-and-store (`lookupAndStorePeppol`, shared by the
click and the worker so there is one ruling, not two): the answer is upserted, and
`customer.peppol_lookup` is recorded **only when it changed**, with the system actor.
A network failure is logged by kind and leaves `checked_at` alone, so that customer
is first in line next cycle. Nothing is ever switched on the billing profile: a
lapsed registration surfaces through the existing `ehf_recipient_not_registered` and
`ehf_available` warnings, and only a person changes `invoiceDelivery`.

**The card's one line.** `CustomerRegistryRecord` gains an optional
`registryUpdatedHint`, and the Registry card shows one line while it is newer than
`fetchedAt`: "The registry reported a change on {date}; this record is from {date}.",
with the existing **Refresh** in the card's header. That is the whole user-visible
surface of this delivery.

**Out of scope, on purpose:** `includeChanges` (the entity is re-read whole);
sub-entities (`underenheter`); an operator "run now" or worker-status endpoint;
rate limiting against Brreg beyond one request at a time; notifying anyone of what a
worker found (the attention list is the notification); a Peppol attention item (the
billing warning is where a lapsed registration belongs).

**Configuration**, all read once at startup by `internal/config`:

| Variable | Default | |
| --- | --- | --- |
| `CUSTOMERS_REGISTRY_FEED_ENABLED` | `1` | `0` → the feed worker is never handed to the runner, so no scheduled Brreg request is made |
| `CUSTOMERS_REGISTRY_FEED_POLL` | `15m` | how often a feed cycle runs |
| `CUSTOMERS_PEPPOL_RECHECK_ENABLED` | `1` | the re-check worker; effective only with `PEPPOL_LOOKUP_ENABLED=1` |
| `CUSTOMERS_PEPPOL_RECHECK_POLL` | `24h` | how often a re-check cycle runs |
| `CUSTOMERS_PEPPOL_RECHECK_AGE` | `720h` | a stored lookup older than this is asked again |

`BRREG_BASE_URL` and `BRREG_TIMEOUT` are reused — a worker refresh is bounded by the
full `BRREG_TIMEOUT`, unlike the create hook's one attempt, because nobody is
waiting on it. Page size, page budget, the sweep batches and the Peppol batch are
constants, not knobs: they bound one cycle's work against a public register.
```

- [ ] **Step 2: `docs/customers.md` — the Peppol section's own table**

The Peppol lookup section's **Configuration** table lists the four `PEPPOL_*`
variables. Add one line at its end, so somebody reading about Peppol finds the
schedule without reading the registry section:

```markdown
| `CUSTOMERS_PEPPOL_RECHECK_ENABLED` / `_POLL` / `_AGE` | `1` / `24h` / `720h` | the background re-check worker, which asks the network again on a schedule — see [Registry workers](#registry-workers) |
```

and, in that section's **The frontend** paragraph or immediately after the "**The
timeline event.**" paragraph, one sentence:

```markdown
**Who asks.** A click is no longer the only thing that asks: the
`customers-peppol-recheck` worker re-asks for an aged answer and for a customer
already set to `ehf` that has never been checked ([Registry workers](#registry-workers)).
It records the same event under the same rule — only when the answer changed — with
the system actor, and it changes nothing on the billing profile.
```

- [ ] **Step 3: `docs/customers.md` — "What comes next"**

Replace the paragraph beginning "**Phase 3 delivery B**, not yet built, is exactly
that schedule:" with:

```markdown
**Phase 3 delivery B** — [Registry workers](#registry-workers) — has since landed on
top of it: Brreg's incremental update feed driving the same fetch-and-store on a
cursor, filling the `registry_updated_hint` column delivery A's own migration
(`00021`) carried but left untouched, with a sweep that both retries a failed refresh
and backfills every Norwegian business customer that never had a record; and
scheduled Peppol re-checks on the same `ehf_available`/`ehf_recipient_not_registered`
warnings a manual check already raises. Registry data in this module is now
maintained rather than merely fetched once. Past that, the remaining gaps are exactly
what [ROADMAP.md's Customers section](../ROADMAP.md#customers) is built around —
`ContactsByEmail` still unused in production, no CSV import/export, no merge
(phase 6) — itself drawn from
[`docs/superpowers/research/2026-09-21-customers-module-next.md`](superpowers/research/2026-09-21-customers-module-next.md),
which also compares this module against the Nordic ERP/accounting and international
CRM/PSA fields it was benchmarked against.
```

Also amend the `## Registry record` section's opening sentence, which currently ends
"see [What comes next](#what-comes-next) for delivery B" — point it at the section
that now exists instead: "delivery B keeps it current on a schedule, see
[Registry workers](#registry-workers)".

- [ ] **Step 4: `ROADMAP.md`**

Under `### Phase 3 — Brreg in full`, change `**Delivery A (done)**` to stay as it is,
and replace the whole `**Delivery B**, not yet built — …` paragraph (and its
`*Unblocks:*` line) with:

```markdown
**Delivery B (done)** — a scheduled refresh from Brreg's incremental update feed
(`GET /oppdateringer/enheter`, exact cursor on `oppdateringsid`, one unfiltered scan
matched against this installation's customers locally), driving the same
fetch-and-store delivery A built and filling `registry_updated_hint`, the column
delivery A's own migration already carried but left untouched. The same cycle sweeps:
records whose last refresh failed (`hint > fetched_at` is the whole retry mechanism)
and Norwegian business customers that never had a record at all, so an installation
that predates delivery A catches up on its own. Beside it, **scheduled re-checks of
Peppol registration**: the click (design D4 of the Peppol delivery) is no longer the
only thing that asks — an aged answer, and a customer already set to `ehf` that was
never checked, are asked again on a schedule, surfacing on the same
`ehf_available`/`ehf_recipient_not_registered` warnings a manual check raises, and
still never switching a customer's delivery method. Both workers elect one replica
per cycle through a Postgres advisory lease and are configured per installation
(`CUSTOMERS_REGISTRY_FEED_*`, `CUSTOMERS_PEPPOL_RECHECK_*`). See
[`docs/customers.md`](docs/customers.md#registry-workers).

*Delivered:* registry data worth relying on instead of a name and a number typed
once, and more behind the one endpoint (`/stats/attention`) and the one event type
(`registry.change`) this module already declared.
```

If the heading `### Phase 3 — Brreg in full` now describes something fully shipped,
mark it `### Phase 3 — Brreg in full (done)`, matching how the other modules' done
phases are marked in this file.

- [ ] **Step 5: `CONTRIBUTING.md`**

Two edits in the background-workers paragraph:

- "Communications contributes the only three today: outbox delivery, retention, and
  attachment cleanup." becomes "Communications contributes three — outbox delivery,
  retention, and attachment cleanup — and Customers two: the Brreg registry-feed
  worker and the Peppol re-check worker, each registered only when its own
  configuration switch is on, so the runner's startup log names exactly what is
  running."
- "Only the retention worker takes an advisory lease, so only it runs on one replica
  at a time. The other two rely on a conditional-update claim…" becomes "Communications'
  retention worker and both of Customers' workers take an advisory lease, so each runs
  on one replica at a time. Communications' other two rely on a conditional-update
  claim — the `UPDATE ... WHERE <the same predicate the candidate select used>` *is*
  the lock — so every replica runs those every cycle and the database decides who
  wins each row. Adding an advisory lock to either would be a defect, not a
  hardening: it is not how the original behaves and the claim already provides the
  exclusion. A lease is what the three lease-holders need because they have no
  per-row claim to fall back on: a retention batch, a feed cursor and a network
  re-check are each one indivisible unit of work for the whole installation."

Leave the paragraph's operation counts and the rest of the file alone.

- [ ] **Step 6: Check and commit**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run translations:check
grep -n "registry-workers" docs/customers.md ROADMAP.md   # every anchor resolves to the new section's heading
printf '%s\n\n%s\n' 'docs(customers): what the registry workers do, and what an operator can set' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-task6
git add docs/customers.md ROADMAP.md CONTRIBUTING.md
git commit -F /tmp/msg-task6 -- docs/customers.md ROADMAP.md CONTRIBUTING.md
git show --stat HEAD && git status --short
```

---

### Task 7: Verify the whole branch and open the PR

**Files:** none — this task only runs things, and then opens the PR.

- [ ] **Step 1: Generation drift**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: nothing changed by either. Anything that appears was committed in the wrong state — commit the fix, do not leave it uncommitted.

- [ ] **Step 2: The full Go suite, vet and lint**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go vet ./... && mise exec -- golangci-lint run && mise exec -- go test -count=1 ./...
```

- [ ] **Step 3: The race detector on four CPUs**

The runner has many cores but the race detector plus this repo's harness needs
pinning, as the previous deliveries did:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
taskset -c 0-3 mise exec -- go test -count=1 -race ./internal/customers/ ./internal/module/ ./internal/db/ ./internal/config/
```
The two worker loops (`Run` with a cancelled context) and the transports' mutexes are
what this is for.

- [ ] **Step 4: The frontend**

```bash
cd /home/anders/projects/vantigo/vantigo && mise run frontend:check
```

- [ ] **Step 5: Normalise the trailers, rebase, push**

Check every commit on the branch ends with the exact trailer and nothing else:

```bash
cd /home/anders/projects/vantigo/vantigo
git log --format='%h %s%n%(trailers:only)' origin/main..HEAD
```
Fix any that differ **before the first push** (`git rebase -i` is unavailable in this
environment — use `git filter-branch`-free approaches: if a trailer is wrong, the
cheapest correct fix is `git reset --soft` back to the offending commit and recommit
by pathspec, which is safe because nothing is pushed yet). Then:

```bash
cd /home/anders/projects/vantigo/vantigo && git fetch origin && git rebase origin/main
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 ./internal/customers/ ./internal/config/ ./internal/db/
cd /home/anders/projects/vantigo/vantigo && git push -u origin feat/customers-registry-workers
```
If the rebase moved anything, re-run Step 1 (generation drift) and Step 4 (the frontend) before pushing. If `main` is already red for reasons that are not ours, say so in the report rather than trying to fix it here.

- [ ] **Step 6: Open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
gh pr create --base main --head feat/customers-registry-workers \
  --title 'feat(customers): registry workers — the record and the Peppol answer stay current on their own' \
  --body-file /tmp/pr-body.md
```
`/tmp/pr-body.md` says what the delivery is, then lists the decisions a reviewer
should look at:

1. **Worker names are `customers-registry-feed` / `customers-peppol-recheck`**, not
   the design doc's `customers.registry-feed` / `customers.peppol-recheck`: the
   existing three workers spell it `<module>-<worker>`, and one log with two
   spellings is the first thing an operator would have to explain.
2. **Both workers are registered only when their switch is on**, rather than always
   registered and skipping inside the cycle, so "off" means no scheduled outbound
   request from this process and the runner's startup log names what is actually
   running.
3. **The Peppol candidate sets are ordered EHF-first**, so on an installation with
   more aged answers than the 100-customer batch the never-checked EHF customers are
   never the ones that do not fit.
4. **The participant equality that D6 requires is applied in Go, not SQL**, so the
   "which participant does this customer resolve to" rule has exactly one
   implementation — which means a cycle may refresh fewer than its batch.
5. **`registryUpdatedHint` is carried onto the record a refresh answers with**, since
   the upsert deliberately never writes that column and a response without it would
   describe a row that still has one.
6. **`CustomerRegistryAddress.countryCode` left `required`** — the delivery A leftover
   the spec names. The server now omits it instead of sending `""`; the frontend
   normaliser maps absent to `""`, so nothing downstream changed.

End the body with the attribution line `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

Never merge. Report the PR URL, the decisions above, and that every new test was
shown able to fail.
