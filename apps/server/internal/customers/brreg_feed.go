package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// This file is the Brreg client's third operation (registry workers
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

// brregFeedRead is what this read is called in an error message
// (validateBrregContentType and readBrregBody, brreg_entity.go), so the two
// reads' failures are distinguishable in a log without either of them owning a
// second copy of the mechanics.
const brregFeedRead = "update feed"

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
			if errors.Is(err, errBrregBody) {
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
		if err := validateBrregContentType(brregFeedRead, contentType, "application/json"); err != nil {
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
// asking for application/json. The capped read is readBrregBody's
// (brreg_entity.go), shared with the entity read: this function owns only the
// request this operation makes.
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

	data, err := readBrregBody(brregFeedRead, resp, brregFeedMaxBodyBytes)
	if err != nil {
		return 0, "", nil, err
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), data, nil
}
