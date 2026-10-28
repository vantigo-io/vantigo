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
	// MailboxEventState carries, and the attendees to count.
	eventsSelect = "id,changeKey,type,subject,body,location,start,end,isAllDay,showAs,sensitivity,isCancelled,attendees"
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
	s, err := m.open(ctx, mailbox, false)
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
		if err := m.client.get(ctx, s.cred, path, &page, noWords, largeAnswer); err != nil {
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
				"tenant", s.cred.tenantID.String(), "bad_ids", badIDs, "unmarked", unmarked)
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
}

// stateOf is ev as the contract answers it, false when its times do not
// read. Key is this installation's key, Foreign a marker of another's (or
// one carrying no key); the times as Delta's (changeOf): a timed event's
// instants in UTC, an all-day event's dates rounded to the nearest
// midnight, the end inclusive; ShowAs the contract's value, "" for one it
// does not name; Private a private or confidential sensitivity; TimeZone "".
func stateOf(ev wholeEvent, prefix string) (contracts.MailboxEventState, bool) {
	change, ok := changeOf(deltaEvent{ID: ev.ID, Type: ev.Type, IsAllDay: ev.IsAllDay, IsCancelled: ev.IsCancelled,
		ShowAs: ev.ShowAs, Start: ev.Start, End: ev.End})
	if !ok {
		return contracts.MailboxEventState{}, false
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
// asked or its times do not read — left out, counted at debug level; 404
// (the event not in that calendar) is not answered; 429 is sent again once,
// alone, after a Retry-After of at most mailboxMaxRetryAfter, then
// errThrottled; any other refusal fails the whole call through
// mailboxRefusal. At most MailboxMarkedMax ids, each a validID; refused
// under cleanup.
func (m *mailboxCalendar) Events(ctx context.Context, mailbox, calendarID string, eventIDs []string) ([]contracts.MailboxEventState, error) {
	if len(eventIDs) > contracts.MailboxMarkedMax {
		return nil, fmt.Errorf("entra: Events takes at most %d ids", contracts.MailboxMarkedMax)
	}
	if calendarID != "" && !validID(calendarID) {
		return nil, errors.New("entra: a calendar id that is no id")
	}
	for _, id := range eventIDs {
		if !validID(id) {
			return nil, errors.New("entra: an event id that is no id")
		}
	}
	s, err := m.open(ctx, mailbox, false)
	if err != nil {
		return nil, err
	}
	states := make([]*contracts.MailboxEventState, len(eventIDs))
	all := make([]int, len(eventIDs))
	for i := range all {
		all[i] = i
	}
	for chunk := range slices.Chunk(all, mailboxBatchSize) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := m.eventsBatch(ctx, s, calendarID, eventIDs, chunk, states); err != nil {
			return nil, err
		}
	}
	out := []contracts.MailboxEventState{}
	for _, st := range states {
		if st != nil {
			out = append(out, *st)
		}
	}
	return out, nil
}

// eventsBatch reads the events at chunk (indices into ids) as one $batch
// and writes the state of each answered into states; the ones answered 429
// are sent once more, alone, after the longest Retry-After among them. The
// answer is read through graphMaxDeltaBody (wholeEvents): twenty bodies,
// which Graph may answer as HTML whatever the Prefer, can outgrow
// graphMaxBatchBody.
func (m *mailboxCalendar) eventsBatch(ctx context.Context, s mailboxSession, calendarID string, ids []string, chunk []int, states []*contracts.MailboxEventState) error {
	prefix := s.keyPrefix()
	params := query(url.Values{"$select": {eventsSelect}, "$expand": {markedExpand}})
	send := chunk
	skipped := 0
	defer func() {
		if skipped > 0 {
			m.client.logger.DebugContext(ctx, "entra: an events read answered events whose ids or times do not read",
				"tenant", s.cred.tenantID.String(), "skipped", skipped)
		}
	}()
	for attempt := 1; ; attempt++ {
		requests := make([]mailboxRequest, len(send))
		for n, i := range send {
			requests[n] = mailboxRequest{ID: strconv.Itoa(i), Method: http.MethodGet,
				URL: s.eventPathIn(calendarID, ids[i]) + "?" + params, Headers: map[string]string{"Prefer": eventsPrefer}}
		}
		var answer batchAnswer
		if err := m.client.post(ctx, s.cred, "/$batch", map[string]any{"requests": requests}, &answer, noWords, wholeEvents); err != nil {
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
				var ev wholeEvent
				if err := json.Unmarshal(r.Body, &ev); err != nil {
					return fmt.Errorf("%w: an event read is not the JSON expected", errTenantUnreachable)
				}
				if ev.ID != ids[i] {
					skipped++
					continue
				}
				st, ok := stateOf(ev, prefix)
				if !ok {
					skipped++
					continue
				}
				states[i] = &st
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
					"tenant", s.cred.tenantID.String(), "status", r.Status, "code", g.Code, "detail", g.Detail)
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
