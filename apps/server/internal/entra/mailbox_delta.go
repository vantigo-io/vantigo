package entra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// This file is the mailbox provider's reads (calendar Outlook sync design
// O9.1–O9.4; delivery B's plan reading 12): Delta, the default calendar's
// calendarView/delta a page at a time, and Marked, the marker read of single
// events. Neither decodes, logs or answers anything of what an event is
// about: Graph answers a delta's subject, body, organizer and attendees, and
// the decoding below names none of them.

// The reads' figures (design O9.1).
const (
	// mailboxDeltaPages is the most pages one Delta call reads; a delta
	// longer is answered cut, its next link to continue with.
	mailboxDeltaPages = 40
)

// mailboxDeltaExpiredCodes are the codes of Graph's 4xx for a delta link it
// no longer takes (research §3, Delta, as checked for B), compared whatever
// their case; a 410 Gone is one whatever its code.
var mailboxDeltaExpiredCodes = []string{
	"syncStateNotFound", "syncStateInvalid", "resyncRequired", "ErrorInvalidSyncStateData",
}

// graphAPIPath is graphAPIBase's path: the version every link of Graph's
// starts with.
var graphAPIPath = strings.TrimPrefix(graphAPIBase, "https://"+graphAPIHost)

// deltaPath is the path, after /v1.0, to send for link, refused unless link
// is a link of this session's mailbox's delta — so a stored link, or one
// Graph answered, can never point the client at another mailbox or off
// Graph. The link must parse as https on Graph's host exactly, with no user
// and no fragment, no percent-encoding in its path, a query, and the path
// /v1.0/users/{oid}/calendarView/delta of this mailbox's oid, compared
// whatever its case: Graph answers calendarview and the oid in either. The
// path sent is ours, built from the oid, with the link's query alone after
// it — nothing of the link's path reaches the request.
func (s mailboxSession) deltaPath(link string) (string, bool) {
	if strings.ContainsAny(link, "#\\ ") {
		return "", false
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host != graphAPIHost || u.User != nil || u.Opaque != "" ||
		u.Fragment != "" || u.RawPath != "" || u.RawQuery == "" ||
		!strings.EqualFold(u.Path, graphAPIPath+userPath(s.oid, "/calendarView/delta")) {
		return "", false
	}
	return userPath(s.oid, "/calendarView/delta?") + u.RawQuery, true
}

// deltaExpired reports whether err is Graph refusing a delta link: a 410,
// or a 4xx naming one of mailboxDeltaExpiredCodes.
func deltaExpired(err error) bool {
	g, ok := asGraphError(err)
	if !ok {
		return false
	}
	if g.Status == http.StatusGone {
		return true
	}
	if g.Status < http.StatusBadRequest || g.Status >= http.StatusInternalServerError {
		return false
	}
	for _, code := range mailboxDeltaExpiredCodes {
		if strings.EqualFold(g.Code, code) || strings.EqualFold(g.Detail, code) {
			return true
		}
	}
	return false
}

// deltaEvent is an event of a delta page as the provider decodes it: these
// fields and no other, so nothing of what the event is about is kept.
type deltaEvent struct {
	ID          string            `json:"id"`
	Removed     json.RawMessage   `json:"@removed"`
	Type        string            `json:"type"`
	IsAllDay    bool              `json:"isAllDay"`
	IsCancelled bool              `json:"isCancelled"`
	ShowAs      string            `json:"showAs"`
	Start       graphDateTimeZone `json:"start"`
	End         graphDateTimeZone `json:"end"`
}

// Delta reads the changes to mailbox's default calendar (design O9.1,
// O9.4). link "" starts a delta afresh, GET
// /users/{oid}/calendarView/delta?startDateTime=…Z&endDateTime=…Z over
// from..to; any other link is followed only when it is this mailbox's delta
// (deltaPath), else refused with a failure, unsent. Each page is asked with
// Prefer: outlook.timezone="UTC", odata.maxpagesize=50 (deltaPage) and its
// @odata.nextLink, checked the same way, followed up to mailboxDeltaPages
// pages: at the @odata.deltaLink the answer is Done with it, else cut with
// the next link. A refused link — 410, or a 4xx with a sync state's code —
// is ErrMailboxDeltaExpired; the rest of the refusals go through
// mailboxRefusal. Refused under cleanup.
func (m *mailboxCalendar) Delta(ctx context.Context, mailbox string, from, to time.Time, link string) (contracts.MailboxDelta, error) {
	if link == "" && (from.IsZero() || !to.After(from)) {
		return contracts.MailboxDelta{}, errors.New("entra: a delta's window ends before it starts")
	}
	s, err := m.open(ctx, mailbox, false, false)
	if err != nil {
		return contracts.MailboxDelta{}, err
	}
	var path string
	if link == "" {
		path = userPath(s.oid, "/calendarView/delta?", query(url.Values{
			"startDateTime": {from.UTC().Format(time.RFC3339)},
			"endDateTime":   {to.UTC().Format(time.RFC3339)},
		}))
	} else {
		var ok bool
		if path, ok = s.deltaPath(link); !ok {
			return contracts.MailboxDelta{}, errors.New("entra: a delta link that is not this mailbox's")
		}
	}
	out := contracts.MailboxDelta{Changes: []contracts.MailboxChange{}}
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return contracts.MailboxDelta{}, err
		}
		var answer struct {
			Value     []deltaEvent `json:"value"`
			NextLink  string       `json:"@odata.nextLink"`
			DeltaLink string       `json:"@odata.deltaLink"`
		}
		if err := m.client.getAs(ctx, s.src, path, &answer, noWords, deltaPage); err != nil {
			if deltaExpired(err) {
				return contracts.MailboxDelta{}, fmt.Errorf("%w: %w", contracts.ErrMailboxDeltaExpired, err)
			}
			return contracts.MailboxDelta{}, mailboxRefusal(err)
		}
		skipped, badIDs := 0, 0
		for _, ev := range answer.Value {
			if !validID(ev.ID) {
				badIDs++
				continue
			}
			change, ok := changeOf(ev)
			if !ok {
				skipped++
				continue
			}
			out.Changes = append(out.Changes, change)
		}
		if skipped > 0 || badIDs > 0 {
			m.client.logger.DebugContext(ctx, "entra: a delta page held events whose ids or times do not read",
				"tenant", s.tenantID.String(), "skipped", skipped, "bad_ids", badIDs)
		}
		switch {
		case answer.DeltaLink != "":
			if _, ok := s.deltaPath(answer.DeltaLink); !ok {
				return contracts.MailboxDelta{}, fmt.Errorf("%w: a delta link off the mailbox's delta", errTenantUnreachable)
			}
			out.Link, out.Done = answer.DeltaLink, true
			return out, nil
		case answer.NextLink != "":
			next, ok := s.deltaPath(answer.NextLink)
			if !ok {
				return contracts.MailboxDelta{}, fmt.Errorf("%w: a next page off the mailbox's delta", errTenantUnreachable)
			}
			if page == mailboxDeltaPages {
				out.Link = answer.NextLink
				return out, nil
			}
			path = next
		default:
			return contracts.MailboxDelta{}, fmt.Errorf("%w: a delta page with neither a next nor a delta link", errTenantUnreachable)
		}
	}
}

// changeOf is ev as the contract answers it, false for one with no id or
// whose times do not read (an id that is no validID is left out before it,
// so the busy rows and Marked never see one) — left out rather than failing the page, which
// would hold the delta at it for ever. A removed event is its id alone.
// Series is an occurrence or an exception; ShowAs the contract's value,
// whatever Graph's case, "" for one it does not name.
func changeOf(ev deltaEvent) (contracts.MailboxChange, bool) {
	if ev.ID == "" {
		return contracts.MailboxChange{}, false
	}
	if len(ev.Removed) > 0 && string(ev.Removed) != "null" {
		return contracts.MailboxChange{EventID: ev.ID, Removed: true}, true
	}
	out := contracts.MailboxChange{
		EventID:   ev.ID,
		Series:    strings.EqualFold(ev.Type, "occurrence") || strings.EqualFold(ev.Type, "exception"),
		Cancelled: ev.IsCancelled,
		AllDay:    ev.IsAllDay,
	}
	for _, v := range showAsValues {
		if strings.EqualFold(ev.ShowAs, v) {
			out.ShowAs = v
		}
	}
	start, okStart := deltaTime(ev.Start)
	end, okEnd := deltaTime(ev.End)
	if !okStart || !okEnd {
		return contracts.MailboxChange{}, false
	}
	if ev.IsAllDay {
		out.StartDate = nearestDay(start)
		out.EndDate = nearestDay(end).AddDate(0, 0, -1)
		if out.EndDate.Before(out.StartDate) {
			out.EndDate = out.StartDate
		}
		return out, true
	}
	out.StartsAt, out.EndsAt = start.UTC(), end.UTC()
	if out.EndsAt.Before(out.StartsAt) {
		out.EndsAt = out.StartsAt
	}
	return out, true
}

// deltaTime is d's instant: its dateTime — seven fractional digits, no
// offset — in its timeZone, UTC as asked; a zone that is not UTC is read as
// an IANA name, and false when it is none — the event is then left out
// (changeOf), never read as UTC and so placed hours off.
func deltaTime(d graphDateTimeZone) (time.Time, bool) {
	loc := time.UTC
	if z := strings.TrimSpace(d.TimeZone); z != "" && !strings.EqualFold(z, "UTC") {
		l, err := time.LoadLocation(z)
		if err != nil {
			return time.Time{}, false
		}
		loc = l
	}
	t, err := time.ParseInLocation(graphLocalTime, d.DateTime, loc)
	return t, err == nil
}

// nearestDay is the civil date of the midnight nearest t in t's zone, at
// midnight UTC: t itself when it is at midnight, the next day from noon on.
func nearestDay(t time.Time) time.Time {
	y, mo, d := t.Add(12 * time.Hour).Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
}

// markedExpand is the $expand that answers an event's marker.
var markedExpand = "singleValueExtendedProperties($filter=id eq '" + mailboxMarker + "')"

// Marked answers, among eventIDs, the events carrying this installation's
// marker with their keys, in the order asked (design O9.2, O9.4): GET
// /users/{oid}/events/{id}?$select=id&$expand=<the marker> in $batch
// requests of twenty, one after another. 200 with the marker's value
// starting keyPrefix is answered; 200 without it, or with another
// installation's, and 404 (the event gone) are not; 429 is sent again once,
// alone, after a Retry-After of at most mailboxMaxRetryAfter, then
// errThrottled; a 403 or a mailbox gone fails the whole call through
// mailboxRefusal; any other refusal fails it as it is. At most
// MailboxMarkedMax ids, each a validID; refused under cleanup.
func (m *mailboxCalendar) Marked(ctx context.Context, mailbox string, eventIDs []string) ([]contracts.MailboxEventRef, error) {
	if len(eventIDs) > contracts.MailboxMarkedMax {
		return nil, fmt.Errorf("entra: Marked takes at most %d ids", contracts.MailboxMarkedMax)
	}
	for _, id := range eventIDs {
		if !validID(id) {
			return nil, errors.New("entra: an event id that is no id")
		}
	}
	s, err := m.open(ctx, mailbox, false, false)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(eventIDs))
	all := make([]int, len(eventIDs))
	for i := range all {
		all[i] = i
	}
	for chunk := range slices.Chunk(all, mailboxBatchSize) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := m.markedBatch(ctx, s, eventIDs, chunk, keys); err != nil {
			return nil, err
		}
	}
	out := []contracts.MailboxEventRef{}
	for i, key := range keys {
		if key != "" {
			out = append(out, contracts.MailboxEventRef{EventID: eventIDs[i], Key: key})
		}
	}
	return out, nil
}

// markedBatch reads the events at chunk (indices into ids) as one $batch and
// writes the key each carries into keys; the ones answered 429 are sent once
// more, alone, after the longest Retry-After among them.
func (m *mailboxCalendar) markedBatch(ctx context.Context, s mailboxSession, ids []string, chunk []int, keys []string) error {
	prefix := s.keyPrefix()
	send := chunk
	for attempt := 1; ; attempt++ {
		requests := make([]mailboxRequest, len(send))
		for n, i := range send {
			requests[n] = mailboxRequest{ID: strconv.Itoa(i), Method: http.MethodGet,
				URL: s.eventPath(ids[i]) + "?" + query(url.Values{"$select": {"id"}, "$expand": {markedExpand}})}
		}
		var answer batchAnswer
		if err := m.client.postAs(ctx, s.src, "/$batch", map[string]any{"requests": requests}, &answer, noWords, largeAnswer); err != nil {
			return mailboxRefusal(err)
		}
		byID := make(map[string]int, len(answer.Responses))
		for n, r := range answer.Responses {
			byID[r.ID] = n
		}
		var throttled []int
		var wait time.Duration
		for _, i := range send {
			n, ok := byID[strconv.Itoa(i)]
			if !ok {
				return fmt.Errorf("%w: the $batch did not answer a read", errTenantUnreachable)
			}
			r := answer.Responses[n]
			switch r.Status {
			case http.StatusOK:
				var ev struct {
					Properties []graphProperty `json:"singleValueExtendedProperties"`
				}
				if err := json.Unmarshal(r.Body, &ev); err != nil {
					return fmt.Errorf("%w: an event read is not the JSON expected", errTenantUnreachable)
				}
				for _, p := range ev.Properties {
					if v, ok := strings.CutPrefix(p.Value, prefix); ok && v != "" && strings.EqualFold(p.ID, mailboxMarker) {
						keys[i] = v
					}
				}
			case http.StatusTooManyRequests:
				d, ok := retryAfter(http.Header{"Retry-After": {batchHeader(r.Headers, "Retry-After")}}, m.client.now())
				if attempt > 1 || !ok || d > mailboxMaxRetryAfter {
					return errThrottled
				}
				throttled = append(throttled, i)
				wait = max(wait, d)
			default:
				g := batchRefusal(r.Status, r.Body)
				m.client.logger.DebugContext(ctx, "entra: Graph refused a mailbox read",
					"tenant", s.tenantID.String(), "status", r.Status, "code", g.Code, "detail", g.Detail)
				if r.Status == http.StatusNotFound && !mailboxGone(g) {
					continue
				}
				return mailboxRefusal(g)
			}
		}
		if len(throttled) == 0 {
			return nil
		}
		if err := m.client.pause(ctx, attempt, wait); err != nil {
			return err
		}
		send = throttled
	}
}

// batchRefusal is the *graphError of a $batch response answered status with
// body: its codes, never its words.
func batchRefusal(status int, body json.RawMessage) *graphError {
	var refusal graphErrorBody
	_ = json.Unmarshal(body, &refusal)
	g := &graphError{Status: status, Code: refusal.Error.Code}
	if len(refusal.Error.Details) > 0 {
		g.Detail = refusal.Error.Details[0].Code
	}
	return g
}
