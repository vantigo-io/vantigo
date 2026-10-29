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

// This file is the mailbox provider's write-back reads (calendar Outlook
// sync design O10.1, O10.2; delivery C's plan reading 10): Versions, the
// copies' ids and change keys a page at a time, and Events, the events the
// write-back takes read whole in $batch requests of twenty. Versions decodes
// an event's id, change key, type and marker and nothing else; Events
// decodes the fields of contracts.MailboxEventState and no other — the
// attendees counted, never an address — and neither logs, nor wraps in an
// error, a word of what an event says: a refusal is logged by its status and
// codes alone, a skipped event by a count.

// The reads' figures and shapes (design O10.1, O10.2).
const (
	// mailboxVersionsPage is the $top Versions asks for: an event's id,
	// change key, type and marker are about half a KiB, so a page of 500 is
	// about 250 KiB (read through graphMaxBatchBody all the same).
	mailboxVersionsPage = 500
	// versionsSelect is what Versions asks of an event.
	versionsSelect = "id,changeKey,type"
	// eventsSelect is what Events asks of an event: the fields
	// MailboxEventState carries, the attendees to count, and the zone an
	// all-day event was laid in (originalStartTimeZone), to read its dates.
	eventsSelect = "id,changeKey,type,subject,body,location,start,end,isAllDay,originalStartTimeZone,showAs,sensitivity,isCancelled,attendees"
	// eventsPrefer is the Prefer header of each of Events' requests: the
	// times in UTC and the body as text — which Graph may not honour, the
	// body's contentType saying which it answered.
	eventsPrefer = `outlook.timezone="UTC", outlook.body-content-type="text"`
)

// listPath is the path, after /v1.0, to send for link — an @odata.nextLink
// Graph answered to a list sent on sent (a path after /v1.0, no query) —
// refused unless link is a page of that very list: B's discipline
// (deltaPath). The link must parse as https on Graph's host exactly, with no
// user and no fragment, a query, and the path /v1.0 + sent, compared
// unescaped and whatever its case. Its path may carry percent-encoding only
// when sent does (a secondary calendar's id escaped), and then only as an
// encoding of the same path. The path sent is ours, sent itself, with the
// link's query alone after it.
func listPath(link, sent string) (string, bool) {
	if strings.ContainsAny(link, "#\\ ") {
		return "", false
	}
	want, err := url.PathUnescape(sent)
	if err != nil {
		return "", false
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host != graphAPIHost || u.User != nil || u.Opaque != "" ||
		u.Fragment != "" || u.RawQuery == "" || (u.RawPath != "" && want == sent) ||
		!strings.EqualFold(u.Path, graphAPIPath+want) {
		return "", false
	}
	return sent + "?" + u.RawQuery, true
}

// seriesType reports whether an event's type is one of a series: an
// occurrence, an exception or the series master, whatever Graph's case.
func seriesType(t string) bool {
	return strings.EqualFold(t, "occurrence") || strings.EqualFold(t, "exception") || strings.EqualFold(t, "seriesMaster")
}

// Versions answers the versions of the calendar's events (design O10.1,
// O10.2). calendarID "" is the default calendar: GET /users/{oid}/events
// filtered on this installation's marker — Find's filter, with start at or
// after from when from is set — each event's Key cut from its marker; to is
// not used. A secondary calendar is GET
// /users/{oid}/calendars/{id}/calendarView over from..to, every event of the
// view with no Key. Both ask $select=id,changeKey,type, $top=500, and
// follow @odata.nextLink only when it is a page of the very list sent
// (listPath), else fail unsent. At most MailboxVersionsMax are answered,
// Complete false when the list was cut. An event whose id is no validID —
// or, on the default calendar, that carries no key of this installation's —
// is left out, counted at debug level, never failing the page. Refusals go
// through mailboxRefusal; refused under cleanup.
func (m *mailboxCalendar) Versions(ctx context.Context, mailbox, calendarID string, from, to time.Time) (contracts.MailboxVersions, error) {
	if calendarID != "" {
		if !validID(calendarID) {
			return contracts.MailboxVersions{}, errors.New("entra: a calendar id that is no id")
		}
		if from.IsZero() || !to.After(from) {
			return contracts.MailboxVersions{}, errors.New("entra: a calendar view's window ends before it starts")
		}
	}
	s, err := m.open(ctx, mailbox, false, false)
	if err != nil {
		return contracts.MailboxVersions{}, err
	}
	prefix := s.keyPrefix()
	params := url.Values{"$select": {versionsSelect}, "$top": {strconv.Itoa(mailboxVersionsPage)}}
	var sent string
	if calendarID == "" {
		sent = userPath(s.oid, "/events")
		filter := "singleValueExtendedProperties/Any(ep: ep/id eq '" + mailboxMarker + "' and startswith(ep/value, '" + prefix + "'))"
		if !from.IsZero() {
			filter += " and start/dateTime ge '" + from.UTC().Format(graphLocalTime) + "'"
		}
		params.Set("$filter", filter)
		params.Set("$expand", markedExpand)
	} else {
		sent = s.calendarPath(calendarID, "/calendarView")
		params.Set("startDateTime", from.UTC().Format(time.RFC3339))
		params.Set("endDateTime", to.UTC().Format(time.RFC3339))
	}
	path := sent + "?" + query(params)
	out := contracts.MailboxVersions{Events: []contracts.MailboxEventVersion{}, Complete: true}
	for {
		if err := ctx.Err(); err != nil {
			return contracts.MailboxVersions{}, err
		}
		var page struct {
			Value []struct {
				ID         string          `json:"id"`
				ChangeKey  string          `json:"changeKey"`
				Type       string          `json:"type"`
				Properties []graphProperty `json:"singleValueExtendedProperties"`
			} `json:"value"`
			NextLink string `json:"@odata.nextLink"`
		}
		if err := m.client.getAs(ctx, s.src, path, &page, noWords, largeAnswer); err != nil {
			return contracts.MailboxVersions{}, mailboxRefusal(err)
		}
		badIDs, unmarked := 0, 0
		for _, ev := range page.Value {
			if !validID(ev.ID) {
				badIDs++
				continue
			}
			v := contracts.MailboxEventVersion{EventID: ev.ID, ChangeKey: ev.ChangeKey, Series: seriesType(ev.Type)}
			if calendarID == "" {
				v.Key = markerKey(ev.Properties, prefix)
				if v.Key == "" {
					unmarked++
					continue
				}
			}
			if len(out.Events) == contracts.MailboxVersionsMax {
				out.Complete = false
				break
			}
			out.Events = append(out.Events, v)
		}
		if badIDs > 0 || unmarked > 0 {
			m.client.logger.DebugContext(ctx, "entra: a versions page held events whose ids or markers do not read",
				"tenant", s.tenantID.String(), "bad_ids", badIDs, "unmarked", unmarked)
		}
		if !out.Complete || page.NextLink == "" {
			return out, nil
		}
		next, ok := listPath(page.NextLink, sent)
		if !ok {
			return contracts.MailboxVersions{}, fmt.Errorf("%w: a next page off the mailbox's list", errTenantUnreachable)
		}
		if len(out.Events) == contracts.MailboxVersionsMax {
			out.Complete = false
			return out, nil
		}
		path = next
	}
}

// markerKey is the key this installation's marker among properties carries —
// its value with prefix cut — "" when there is none.
func markerKey(properties []graphProperty, prefix string) string {
	for _, p := range properties {
		if v, ok := strings.CutPrefix(p.Value, prefix); ok && v != "" && strings.EqualFold(p.ID, mailboxMarker) {
			return v
		}
	}
	return ""
}

// wholeEvent is an event as Events decodes it: these fields and no other.
// The attendees are decoded as empty objects — their count alone, never an
// address, is kept.
type wholeEvent struct {
	ID          string            `json:"id"`
	ChangeKey   string            `json:"changeKey"`
	Type        string            `json:"type"`
	Subject     string            `json:"subject"`
	Body        graphItemBody     `json:"body"`
	Location    graphLocation     `json:"location"`
	IsAllDay    bool              `json:"isAllDay"`
	Start       graphDateTimeZone `json:"start"`
	End         graphDateTimeZone `json:"end"`
	ShowAs      string            `json:"showAs"`
	Sensitivity string            `json:"sensitivity"`
	IsCancelled bool              `json:"isCancelled"`
	Attendees   []struct{}        `json:"attendees"`
	Properties  []graphProperty   `json:"singleValueExtendedProperties"`
	// OriginalStartTimeZone is the zone the event was laid in — a Windows
	// name, or an IANA one — which an all-day event's dates are read in.
	OriginalStartTimeZone string `json:"originalStartTimeZone"`
}

// stateOf is ev as the contract answers it, false when its times do not
// read. Key is this installation's key, Foreign a marker of another's (or
// one carrying no key); the times as Delta's (changeOf): a timed event's
// instants in UTC; an all-day event's dates the dates its UTC instants fall
// on in the zone it was laid in (originalStartTimeZone, a Windows name
// mapped through windowsZones, or an IANA name), the end inclusive — or,
// when that zone does not map to a location, rounded to the nearest
// midnight as Delta's are; ShowAs the contract's value, "" for one it does
// not name; Private a private or confidential sensitivity; TimeZone "".
func stateOf(ev wholeEvent, prefix string) (contracts.MailboxEventState, bool) {
	change, ok := changeOf(deltaEvent{ID: ev.ID, Type: ev.Type, IsAllDay: ev.IsAllDay, IsCancelled: ev.IsCancelled,
		ShowAs: ev.ShowAs, Start: ev.Start, End: ev.End})
	if !ok {
		return contracts.MailboxEventState{}, false
	}
	if loc := zoneLocation(ev.OriginalStartTimeZone); ev.IsAllDay && loc != nil {
		start, _ := deltaTime(ev.Start)
		end, _ := deltaTime(ev.End)
		change.StartDate = civilIn(start, loc)
		change.EndDate = civilIn(end, loc).AddDate(0, 0, -1)
		if change.EndDate.Before(change.StartDate) {
			change.EndDate = change.StartDate
		}
	}
	out := contracts.MailboxEventState{
		EventID:   ev.ID,
		ChangeKey: ev.ChangeKey,
		Key:       markerKey(ev.Properties, prefix),
		Series:    seriesType(ev.Type),
		Cancelled: ev.IsCancelled,
		Meeting:   len(ev.Attendees) > 0,
		TextBody:  strings.EqualFold(ev.Body.ContentType, "text"),
		Event: contracts.MailboxEvent{
			Subject:   ev.Subject,
			Body:      ev.Body.Content,
			Location:  ev.Location.DisplayName,
			AllDay:    change.AllDay,
			StartDate: change.StartDate,
			EndDate:   change.EndDate,
			StartsAt:  change.StartsAt,
			EndsAt:    change.EndsAt,
			ShowAs:    change.ShowAs,
			Private:   strings.EqualFold(ev.Sensitivity, "private") || strings.EqualFold(ev.Sensitivity, "confidential"),
		},
	}
	if out.Key == "" {
		for _, p := range ev.Properties {
			if strings.EqualFold(p.ID, mailboxMarker) && p.Value != "" {
				out.Foreign = true
			}
		}
	}
	return out, true
}

// zoneLocation is the location of a zone Graph names: a Windows name
// through windowsZones (CLDR's table, whatever the name's case), else an
// IANA name; nil for "" or a zone that is neither.
func zoneLocation(zone string) *time.Location {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return nil
	}
	name, ok := windowsZones[zone]
	if !ok {
		for w, iana := range windowsZones {
			if strings.EqualFold(w, zone) {
				name, ok = iana, true
				break
			}
		}
	}
	if !ok {
		name = zone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil
	}
	return loc
}

// civilIn is the civil date t falls on in loc, at midnight UTC.
func civilIn(t time.Time, loc *time.Location) time.Time {
	y, mo, d := t.In(loc).Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, time.UTC)
}

// eventPathIn is the event id read through its calendar's own path: the
// default calendar's /users/{oid}/calendar/events/{id}, else
// /users/{oid}/calendars/{cid}/events/{id} — so an event moved to another
// calendar or folder is not found there.
func (s mailboxSession) eventPathIn(calendarID, id string) string {
	if calendarID == "" {
		return userPath(s.oid, "/calendar/events/", url.PathEscape(id))
	}
	return s.calendarPath(calendarID, "/events/", url.PathEscape(id))
}

// Events reads whole, among eventIDs, the events of the calendar
// (calendarID "" the default one), in the order asked (design O10.2): GET
// of each through the calendar's own path (eventPathIn) with
// $select=<eventsSelect> and the marker expanded, each request carrying
// Prefer: <eventsPrefer>, in $batch requests of twenty, one after another.
// 200 is answered (stateOf), unless the event answered is not the one
// asked, its body is not the JSON expected or its times do not read —
// skipped, counted at debug level; 404 (the event not in that calendar, not
// the mailbox gone) is answered in Gone; a 403 of one event is skipped too
// (a refusal of that event alone, never ErrMailboxDenied — the mailbox's
// own refusal is the $batch's, or Versions'); 429 is sent again once,
// alone, after a Retry-After of at most mailboxMaxRetryAfter, then
// errThrottled; any other refusal fails the whole call through
// mailboxRefusal. A $batch whose answer is larger than the client reads, or
// is not the JSON expected, is sent again one id at a time, and an id whose
// read alone still is is skipped, counted. A refusal of the $batch POST
// itself goes through mailboxRefusal. A skipped id is neither answered nor
// Gone. At most MailboxMarkedMax ids, each a validID; refused under
// cleanup.
func (m *mailboxCalendar) Events(ctx context.Context, mailbox, calendarID string, eventIDs []string) (contracts.MailboxEventsRead, error) {
	if len(eventIDs) > contracts.MailboxMarkedMax {
		return contracts.MailboxEventsRead{}, fmt.Errorf("entra: Events takes at most %d ids", contracts.MailboxMarkedMax)
	}
	if calendarID != "" && !validID(calendarID) {
		return contracts.MailboxEventsRead{}, errors.New("entra: a calendar id that is no id")
	}
	for _, id := range eventIDs {
		if !validID(id) {
			return contracts.MailboxEventsRead{}, errors.New("entra: an event id that is no id")
		}
	}
	s, err := m.open(ctx, mailbox, false, false)
	if err != nil {
		return contracts.MailboxEventsRead{}, err
	}
	r := eventsRead{states: make([]*contracts.MailboxEventState, len(eventIDs)), gone: make([]bool, len(eventIDs))}
	all := make([]int, len(eventIDs))
	for i := range all {
		all[i] = i
	}
	for chunk := range slices.Chunk(all, mailboxBatchSize) {
		if err := ctx.Err(); err != nil {
			return contracts.MailboxEventsRead{}, err
		}
		if err := m.eventsBatch(ctx, s, calendarID, eventIDs, chunk, &r); err != nil {
			return contracts.MailboxEventsRead{}, err
		}
	}
	if r.skipped > 0 {
		m.client.logger.DebugContext(ctx, "entra: an events read skipped events it could not read",
			"tenant", s.tenantID.String(), "skipped", r.skipped)
	}
	out := contracts.MailboxEventsRead{Events: []contracts.MailboxEventState{}, Gone: []string{}}
	for i, st := range r.states {
		switch {
		case st != nil:
			out.Events = append(out.Events, *st)
		case r.gone[i]:
			out.Gone = append(out.Gone, eventIDs[i])
		}
	}
	return out, nil
}

// eventsRead is an Events call's answers so far, by the index of the id
// asked: the state read, or gone; neither when it was skipped.
type eventsRead struct {
	states  []*contracts.MailboxEventState
	gone    []bool
	skipped int
}

// eventsBatch reads the events at chunk (indices into ids) as one $batch
// into r; the ones answered 429 are sent once more, alone, after the
// longest Retry-After among them. The answer is read through
// graphMaxDeltaBody (wholeEvents): twenty bodies, which Graph may answer as
// HTML whatever the Prefer, can outgrow graphMaxBatchBody. One that
// outgrows even that, or does not decode, is sent again an id at a time;
// an id that alone still does is skipped.
func (m *mailboxCalendar) eventsBatch(ctx context.Context, s mailboxSession, calendarID string, ids []string, chunk []int, r *eventsRead) error {
	prefix := s.keyPrefix()
	params := query(url.Values{"$select": {eventsSelect}, "$expand": {markedExpand}})
	send := chunk
	for attempt := 1; ; attempt++ {
		requests := make([]mailboxRequest, len(send))
		for n, i := range send {
			requests[n] = mailboxRequest{ID: strconv.Itoa(i), Method: http.MethodGet,
				URL: s.eventPathIn(calendarID, ids[i]) + "?" + params, Headers: map[string]string{"Prefer": eventsPrefer}}
		}
		var answer batchAnswer
		if err := m.client.postAs(ctx, s.src, "/$batch", map[string]any{"requests": requests}, &answer, noWords, wholeEvents); err != nil {
			if !unreadableAnswer(err) {
				return mailboxRefusal(err)
			}
			if len(send) == 1 {
				r.skipped++
				return nil
			}
			for _, i := range send {
				if err := m.eventsBatch(ctx, s, calendarID, ids, []int{i}, r); err != nil {
					return err
				}
			}
			return nil
		}
		byID := make(map[string]int, len(answer.Responses))
		for n, a := range answer.Responses {
			byID[a.ID] = n
		}
		var throttled []int
		var wait time.Duration
		for _, i := range send {
			n, ok := byID[strconv.Itoa(i)]
			if !ok {
				return fmt.Errorf("%w: the $batch did not answer a read", errTenantUnreachable)
			}
			a := answer.Responses[n]
			switch a.Status {
			case http.StatusOK:
				var ev wholeEvent
				if err := json.Unmarshal(a.Body, &ev); err != nil || ev.ID != ids[i] {
					r.skipped++
					continue
				}
				st, ok := stateOf(ev, prefix)
				if !ok {
					r.skipped++
					continue
				}
				r.states[i] = &st
			case http.StatusTooManyRequests:
				d, ok := retryAfter(http.Header{"Retry-After": {batchHeader(a.Headers, "Retry-After")}}, m.client.now())
				if attempt > 1 || !ok || d > mailboxMaxRetryAfter {
					return errThrottled
				}
				throttled = append(throttled, i)
				wait = max(wait, d)
			default:
				g := batchRefusal(a.Status, a.Body)
				m.client.logger.DebugContext(ctx, "entra: Graph refused a mailbox read",
					"tenant", s.tenantID.String(), "status", a.Status, "code", g.Code, "detail", g.Detail)
				switch {
				case mailboxGone(g):
					return mailboxRefusal(g)
				case a.Status == http.StatusNotFound:
					r.gone[i] = true
					continue
				case a.Status == http.StatusForbidden:
					r.skipped++
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

// unreadableAnswer reports whether err is a $batch answered but not read:
// larger than the client reads, or not the JSON expected.
func unreadableAnswer(err error) bool {
	if errors.Is(err, errResponseTooLarge) {
		return true
	}
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	return errors.As(err, &syntax) || errors.As(err, &typ)
}
