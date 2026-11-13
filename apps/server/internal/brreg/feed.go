package brreg

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

// This file is the client's third operation: Enhetsregisteret's incremental
// update feed, which answers "which entities changed" so a caller never has
// to ask about entities that did not.
//
// Two things about it are load-bearing and neither is obvious from the URL:
//
//  1. **oppdateringsid is inclusive.** "From and including", in the registry's
//     own words, so resuming from the last id already processed would re-read
//     that entry forever. A caller's stored cursor is therefore "the last id
//     processed PLUS ONE", and this file sends whatever it is given without
//     adjusting it — one place owns the arithmetic, the caller's, which also
//     decides a page was processed.
//  2. **An empty result has no _embedded key at all**, with page.totalElements
//     0. That is zero updates, not a malformed body — the same ruling the
//     search applies to its own missing _embedded. Treating it as an error
//     would make an idle installation retry forever and never advance.
//
// Unlike the entity read there is no media-type negotiation here: the feed
// answers plain application/json, so a status is checked before a content type
// rather than after (there is no 406 to explain).

const (
	// feedPath is the update feed's collection
	// (GET {base}/enhetsregisteret/api/oppdateringer/enheter).
	feedPath = "/enhetsregisteret/api/oppdateringer/enheter"

	// feedMaxBodyBytes caps one page at 4 MiB. A page of 1000 entries is
	// ~200 KiB (the live feed's entries are about 200 bytes each): a response
	// past it is not a busier day at the registry, it is something to refuse
	// rather than buffer.
	feedMaxBodyBytes = 4 << 20

	// feedDateLayout is the registry's own dato format, documented as
	// yyyy-MM-dd'T'HH:mm:ss.SSS'Z' — milliseconds and a literal Z, not
	// RFC 3339 in general: a value with an offset instead of Z is rejected.
	feedDateLayout = "2006-01-02T15:04:05.000Z"

	// feedRead is what this read is called in an error message
	// (validateContentType and readBody, entity.go).
	feedRead = "update feed"
)

// FeedQuery is one request's position in the feed. Exactly one of the two
// forms is sent: UpdateID when there is one (exact, inclusive), otherwise
// Since (a moment to join the feed at). Size is the page size; the registry
// accepts up to 10000.
type FeedQuery struct {
	UpdateID *int64
	Since    time.Time
	Size     int
}

// path is the request path for this query.
func (q FeedQuery) path() string {
	v := url.Values{}
	if q.UpdateID != nil {
		v.Set("oppdateringsid", strconv.FormatInt(*q.UpdateID, 10))
	} else {
		v.Set("dato", q.Since.UTC().Format(feedDateLayout))
	}
	v.Set("size", strconv.Itoa(q.Size))
	return feedPath + "?" + v.Encode()
}

// FeedEntry is one update the registry reported: which entity, when, and what
// kind of change. ChangeType is carried through verbatim — "Ny", "Endring",
// "Sletting" (struck from the register), "Fjernet" (removed from open data) and
// "Ukjent" (older entries) — so this package does not narrow the set or reject
// a value the registry adds later.
type FeedEntry struct {
	UpdateID           int64
	Date               time.Time
	OrganisationNumber string
	ChangeType         string
}

// FeedPage is one page of the feed. There is deliberately nothing else on it:
// a reader's stopping rule is a SHORT page (fewer entries than it asked for),
// not the response's _links.next — a cursor that only advances over entries
// actually processed cannot be led astray by a link.
type FeedPage struct {
	Entries []FeedEntry
}

// feedWire is the response's own field names. Embedded is a pointer because
// its absence is the empty result (this file's header, point 2).
type feedWire struct {
	Embedded *struct {
		Updated []feedEntryWire `json:"oppdaterteEnheter"`
	} `json:"_embedded"`
}

type feedEntryWire struct {
	UpdateID           int64  `json:"oppdateringsid"`
	Date               string `json:"dato"`
	OrganisationNumber string `json:"organisasjonsnummer"`
	ChangeType         string `json:"endringstype"`
}

// parseFeedPage decodes one page. A malformed dato is a malformed body, not a
// dropped entry: an entry whose date this package invented would make a record
// look fresher or staler than the registry said.
func parseFeedPage(body []byte) (FeedPage, error) {
	var wire feedWire
	if err := json.Unmarshal(body, &wire); err != nil {
		return FeedPage{}, fmt.Errorf("brreg: update feed body could not be decoded: %w", err)
	}
	if wire.Embedded == nil {
		return FeedPage{}, nil
	}
	entries := make([]FeedEntry, 0, len(wire.Embedded.Updated))
	for _, e := range wire.Embedded.Updated {
		date, err := time.Parse(feedDateLayout, e.Date)
		if err != nil {
			return FeedPage{}, fmt.Errorf("brreg: malformed update feed date %q: %w", e.Date, err)
		}
		entries = append(entries, FeedEntry{
			UpdateID:           e.UpdateID,
			Date:               date.UTC(),
			OrganisationNumber: strings.TrimSpace(e.OrganisationNumber),
			ChangeType:         strings.TrimSpace(e.ChangeType),
		})
	}
	return FeedPage{Entries: entries}, nil
}

// Updates reads one page of the feed from q, bounded overall by the client's
// timeout and per attempt by AttemptTimeout, retrying a transport error or a
// retryable status with the backoff between attempts — the same policy the
// search and the entity read use. Every failure wraps ErrUnavailable, so a
// caller has exactly one thing to decide: stop, and leave its cursor alone.
func (c *Client) Updates(ctx context.Context, q FeedQuery) (FeedPage, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	path := q.path()

	var lastErr error
	for attempt := 0; attempt <= retryAttempts; attempt++ {
		if attempt > 0 {
			if !waitBackoff(ctx, c.backoff(attempt)) {
				break
			}
		}
		status, contentType, body, err := c.feedAttempt(ctx, path)
		if err != nil {
			if errors.Is(err, errBody) {
				return FeedPage{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
			}
			lastErr = err
			continue
		}
		if isRetryableStatus(status) {
			lastErr = fmt.Errorf("brreg responded %d", status)
			continue
		}
		if !isSuccessStatus(status) {
			// A 400 is the registry telling the caller its own request was
			// wrong (an oppdateringsid past int32, say): another attempt cannot
			// improve it, and it is still "the feed could not be read".
			return FeedPage{}, fmt.Errorf("%w: brreg responded %d", ErrUnavailable, status)
		}
		if err := validateContentType(feedRead, contentType, "application/json"); err != nil {
			return FeedPage{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		page, perr := parseFeedPage(body)
		if perr != nil {
			return FeedPage{}, fmt.Errorf("%w: %w", ErrUnavailable, perr)
		}
		return page, nil
	}
	return FeedPage{}, fmt.Errorf("%w: %w", ErrUnavailable, lastErr)
}

// feedAttempt performs one GET for path, bounded by AttemptTimeout, asking for
// application/json. The capped read is readBody's (entity.go), shared with the
// entity read.
func (c *Client) feedAttempt(ctx context.Context, path string) (status int, contentType string, body []byte, err error) {
	attemptCtx, cancel := context.WithTimeout(ctx, AttemptTimeout)
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

	data, err := readBody(feedRead, resp, feedMaxBodyBytes)
	if err != nil {
		return 0, "", nil, err
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), data, nil
}
