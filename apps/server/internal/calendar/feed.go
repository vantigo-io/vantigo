package calendar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the feed itself (calendar feed design D2–D5): GET
// /feed/{token}.ics, the module's one anonymous GET with its token in the
// path, answering the person's calendar as iCalendar. The token is found by
// its hash; a token nobody holds is the bare 404. The window is today −3
// months to today +12 in the business zone, and what is in it is the
// person's own: the appointments they own or are on with any answer but
// declined, never a cancelled one, and the providers' items under each
// provider's own gate, asked on the person's behalf through contracts.AsUser
// — never a colleague's, a subscription's or a public holiday.

// The feed's window, in months either side of today (D3).
const (
	feedMonthsBack  = 3
	feedMonthsAhead = 12
)

// feedMaxBytes is the most a feed is: Google's import limit, rounded to a
// MiB. Past it the farthest projected items are dropped (D3).
const feedMaxBytes = 1 << 20

// The feed's response headers (D4): a client may keep the calendar and must
// ask again; the file is shown, named vantigo.ics.
const (
	feedCacheControl = "private, no-cache"
	feedDisposition  = `inline; filename="vantigo.ics"`
)

// GetCalendarFeedByFile A calendar address's feed
// (GET /api/v1/calendar/feed/{file})
//
// file is the token with the .ics suffix, one path segment (D2): without the
// suffix, or with nothing before it, it is the 404 a token nobody holds
// gets. A request that answers 200 or 304 writes last_used_at; If-None-Match
// naming the calendar's ETag, or *, is the 304.
func (s *server) GetCalendarFeedByFile(ctx context.Context, req gen.GetCalendarFeedByFileRequestObject) (gen.GetCalendarFeedByFileResponseObject, error) {
	token, ok := strings.CutSuffix(req.File, feedSuffix)
	if !ok || token == "" {
		return gen.GetCalendarFeedByFile404Response{}, nil
	}
	hash := tokenSHA256(token)
	row, err := s.queries().GetFeedTokenByHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.GetCalendarFeedByFile404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("calendar: find a calendar address: %w", err)
	}
	now := s.deps.Clock()
	body, err := s.feedBody(ctx, row, now)
	if errors.Is(err, errSourceFailed) {
		return gen.GetCalendarFeedByFile503ApplicationProblemPlusJSONResponse(sourceUnavailable()), nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.queries().TouchFeedToken(ctx, store.TouchFeedTokenParams{Now: now, TokenSha256: hash}); err != nil {
		return nil, fmt.Errorf("calendar: record a calendar address's read: %w", err)
	}
	etag, cache := feedETag(body), feedCacheControl
	if req.Params.IfNoneMatch != nil && etagMatches(*req.Params.IfNoneMatch, etag) {
		return gen.GetCalendarFeedByFile304Response{
			Headers: gen.GetCalendarFeedByFile304ResponseHeaders{ETag: &etag, CacheControl: &cache},
		}, nil
	}
	disposition := feedDisposition
	return gen.GetCalendarFeedByFile200TextcalendarCharsetUtf8Response{
		Body:          strings.NewReader(body),
		ContentLength: int64(len(body)),
		Headers: gen.GetCalendarFeedByFile200ResponseHeaders{
			ETag: &etag, CacheControl: &cache, ContentDisposition: &disposition,
		},
	}, nil
}

// feedBody is the calendar row's address answers at now: the window, the
// appointments, the providers' items and the object, cut to feedMaxBytes by
// dropping the farthest projected items a quarter at a time; the
// appointments are never dropped.
func (s *server) feedBody(ctx context.Context, row store.CalendarFeedToken, now time.Time) (string, error) {
	loc, err := s.calendarZone(ctx)
	if err != nil {
		return "", fmt.Errorf("calendar: read the business time zone: %w", err)
	}
	today, err := s.calendarToday(ctx, now)
	if err != nil {
		return "", fmt.Errorf("calendar: read today: %w", err)
	}
	from, to := civil(today).AddDate(0, -feedMonthsBack, 0), civil(today).AddDate(0, feedMonthsAhead, 0)
	w := window{from: from, to: to, loc: loc, fromAt: wallClock(from, loc), toAt: wallClock(addDays(to, 1), loc)}
	in := feedInput{busy: row.Variant == string(gen.FeedVariantBusy), lang: langEnglish, loc: loc, now: now}

	in.appts, err = s.feedAppointments(ctx, row.UserID, w, in.busy)
	if err != nil {
		return "", err
	}
	in.projected, err = s.sourceItemsIn(contracts.AsUser(withoutRequest{ctx}, row.UserID), row.UserID, w, nil)
	if err != nil {
		return "", err
	}
	if in.busy {
		u, err := s.usersUser(ctx, row.UserID)
		if err != nil {
			return "", fmt.Errorf("calendar: read the feed's owner: %w", err)
		}
		if u != nil {
			in.lang = languageOf(u.PreferredLanguage)
		}
	}
	byDistance(in.projected, civil(today), loc)

	body, err := s.buildFeed(in)
	dropped := 0
	for err == nil && len(body) > feedMaxBytes && len(in.projected) > 0 {
		n := max(len(in.projected)/4, 1)
		in.projected = in.projected[:len(in.projected)-n]
		dropped += n
		body, err = s.buildFeed(in)
	}
	if err != nil {
		return "", err
	}
	if dropped > 0 {
		s.deps.Logger.InfoContext(ctx, "calendar: the feed was over 1 MiB; the farthest calendar source items were left out",
			"user_id", row.UserID.String(), "variant", row.Variant, "dropped", dropped, "bytes", len(body))
	}
	return body, nil
}

// feedAppointments is userID's appointments the feed writes, with their
// exceptions: owned or attended with any answer but declined, not cancelled
// (ListAppointmentsInWindow leaves those out), and — for the busy variant —
// busy. A single one is in when it overlaps the window, a series when one of
// its occurrences does; a series is written whole, as its rule.
func (s *server) feedAppointments(ctx context.Context, userID uuid.UUID, w window, busy bool) ([]feedAppointment, error) {
	q := s.queries()
	rows, err := q.ListAppointmentsInWindow(ctx, store.ListAppointmentsInWindowParams{
		UserID: userID, FromAt: w.fromAt.Add(-24 * time.Hour), ToAt: w.toAt.Add(24 * time.Hour),
		FromDate: pgDate(addDays(w.from, -seriesReachDays)), ToDate: pgDate(addDays(w.to, 1)),
	})
	if err != nil {
		return nil, fmt.Errorf("calendar: read the feed's appointments: %w", err)
	}
	var appts []store.CalendarAppointment
	var recurring []int64
	for _, r := range rows {
		a := r.CalendarAppointment
		if r.MyStatus != nil && *r.MyStatus == "declined" {
			continue
		}
		if busy && a.Transparency != transparencyBusy {
			continue
		}
		appts = append(appts, a)
		if a.Rrule != nil {
			recurring = append(recurring, a.ID)
		}
	}
	exceptions := map[int64][]store.CalendarAppointmentException{}
	if len(recurring) > 0 {
		excs, err := q.ListExceptions(ctx, recurring)
		if err != nil {
			return nil, fmt.Errorf("calendar: read the feed's exceptions: %w", err)
		}
		for _, e := range excs {
			exceptions[e.AppointmentID] = append(exceptions[e.AppointmentID], e)
		}
	}
	var out []feedAppointment
	for _, a := range appts {
		occs, err := occurrencesIn(a, exceptions[a.ID], w)
		if err != nil {
			return nil, err
		}
		if len(occs) > 0 {
			out = append(out, feedAppointment{appt: a, excs: exceptions[a.ID]})
		}
	}
	return out, nil
}

// byDistance orders projected items by their distance from today in loc,
// nearest first — one touching today is nowhere — keeping the build order
// among equals, so the size rule drops the farthest.
func byDistance(list []gen.CalendarItem, today time.Time, loc *time.Location) {
	dayStart, dayEnd := wallClock(today, loc), wallClock(addDays(today, 1), loc)
	distance := func(it gen.CalendarItem) time.Duration {
		var start, end time.Time
		switch {
		case it.AllDay && it.StartDate != nil && it.EndDate != nil:
			start, end = wallClock(civil(it.StartDate.Time), loc), wallClock(addDays(civil(it.EndDate.Time), 1), loc)
		case it.StartsAt != nil && it.EndsAt != nil:
			start, end = *it.StartsAt, *it.EndsAt
		default:
			return 0
		}
		switch {
		case !end.After(dayStart):
			return dayStart.Sub(end)
		case !start.Before(dayEnd):
			return start.Sub(dayEnd)
		}
		return 0
	}
	sort.SliceStable(list, func(i, j int) bool { return distance(list[i]) < distance(list[j]) })
}

// feedETag is the feed's ETag (D4): the quoted hex SHA-256 of body with
// every DTSTAMP line left out, so a calendar that has not changed has the
// same tag from one hour to the next. A DTSTAMP is short and never folds,
// and a folded line's continuation starts with a space, so no other line
// starts with the name.
func feedETag(body string) string {
	h := sha256.New()
	for _, line := range strings.SplitAfter(body, "\r\n") {
		if strings.HasPrefix(line, "DTSTAMP") {
			continue
		}
		h.Write([]byte(line))
	}
	return `"` + hex.EncodeToString(h.Sum(nil)) + `"`
}

// etagMatches reports whether an If-None-Match header names etag: *, or a
// list of tags one of which is it, compared weakly as RFC 9110 has
// If-None-Match compare them.
func etagMatches(header, etag string) bool {
	for _, tag := range strings.Split(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
			return true
		}
	}
	return false
}

// withoutRequest is a context with the request the router attached to it
// (contracts.WithRequest) taken out — its deadline, its cancellation and
// every other value kept. HasPermission checks a request in the context
// before anything else, and the feed's request is anonymous; without it,
// the AsUser mark is what a provider's gate reads (D5).
type withoutRequest struct{ context.Context }

func (c withoutRequest) Value(key any) any {
	v := c.Context.Value(key)
	switch v.(type) {
	case *http.Request, http.ResponseWriter:
		return nil
	}
	return v
}
