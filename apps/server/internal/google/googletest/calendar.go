package googletest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// This file is the fake's Calendar v3 (Google Workspace design G30–G32,
// delivery E): what www.googleapis.com/calendar/v3 answers a person's own
// access token, by the token's subject — the account's Google id.
//
//   - calendars.insert, get and delete of the secondary calendars, which
//     need calendar.app.created (DenyCalendarCreate refuses the insert 403,
//     as a grant without it would, U8);
//   - events.insert, patch, delete, get and list on the primary calendar
//     (calendar.events) and on a secondary one; list takes
//     privateExtendedProperty, timeMin and timeMax (an event's end after
//     timeMin, its start before timeMax), showDeleted, maxResults and
//     pageToken, and answers a nextSyncToken on its last page unless the
//     list named a private property or NoSyncToken is set (U7); a syncToken
//     answers every change since, deleted events included, unwindowed — 410
//     once ExpireSyncTokens forgot it;
//   - the batch endpoint, /batch/calendar/v3: a multipart/mixed request whose
//     parts are answered as above, each in a multipart/mixed part of its own;
//   - events.watch on the primary calendar, answering the expiry ChannelLife
//     sets (seven days when zero), and channels.stop.
//
// RefuseCalendar answers every call of a person's token 401. Every refusal
// carries Google's words, which a test proves are never repeated.

// The calendar scopes the fake checks.
const (
	ScopeCalendarEvents     = "https://www.googleapis.com/auth/calendar.events"
	ScopeCalendarAppCreated = "https://www.googleapis.com/auth/calendar.app.created"
)

// EventTime is an event's start or end: a date (all-day) or an instant and
// its zone.
type EventTime struct {
	Date     string `json:"date,omitempty"`
	DateTime string `json:"dateTime,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

// Attendee is one attendee of an event.
type Attendee struct {
	Email          string `json:"email,omitempty"`
	Self           bool   `json:"self,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
}

// Event is one event of a calendar as the fake keeps it.
type Event struct {
	ID               string
	Etag             string
	Status           string // confirmed | cancelled
	Summary          string
	Description      string
	Location         string
	Start, End       EventTime
	Transparency     string
	EventType        string
	Visibility       string
	RecurringEventID string
	Attendees        []Attendee
	Private          map[string]string
	// updated is the calendar's change counter when it last changed.
	updated int64
}

// Channel is one push channel events.watch opened.
type Channel struct {
	ID, Token, Address, ResourceID, Owner string
	Expires                               time.Time
}

// calendar is one calendar of a person.
type calendar struct {
	id      string
	summary string
	events  map[string]*Event
	order   []string
	seq     int64
}

// syncPoint is what a sync token stands for.
type syncPoint struct {
	owner, calendar string
	seq             int64
}

// calendarState is the fake's calendars, under its lock.
type calendarState struct {
	calendars map[string]map[string]*calendar // owner → calendar id → calendar
	syncs     map[string]syncPoint
	channels  map[string]*Channel
	refused   map[string]bool
	stopped   []string
	ids       int
	// NoSyncToken, ChannelLife and DenyCalendarCreate are set through the
	// fake's methods.
	noSyncToken  bool
	channelLife  time.Duration
	denyCalendar bool
	throttle     int
}

// SetNoSyncToken makes a windowed first list answer no nextSyncToken (U7).
func (g *FakeGoogle) SetNoSyncToken(on bool) {
	g.Configure(func(g *FakeGoogle) { g.cal.noSyncToken = on })
}

// SetChannelLife is the life events.watch grants a channel.
func (g *FakeGoogle) SetChannelLife(d time.Duration) {
	g.Configure(func(g *FakeGoogle) { g.cal.channelLife = d })
}

// DenyCalendarCreate makes calendars.insert answer 403, as a grant without
// calendar.app.created would (U8).
func (g *FakeGoogle) DenyCalendarCreate(on bool) {
	g.Configure(func(g *FakeGoogle) { g.cal.denyCalendar = on })
}

// RefuseCalendar makes every Calendar call of owner's tokens answer 401, as
// Google does for a token it no longer takes.
func (g *FakeGoogle) RefuseCalendar(owner string, on bool) {
	g.Configure(func(g *FakeGoogle) {
		if g.cal.refused == nil {
			g.cal.refused = map[string]bool{}
		}
		g.cal.refused[owner] = on
	})
}

// ThrottleParts makes the next n parts of batches answer 403
// rateLimitExceeded, as Calendar answers its usage limits.
func (g *FakeGoogle) ThrottleParts(n int) { g.Configure(func(g *FakeGoogle) { g.cal.throttle = n }) }

// ExpireSyncTokens forgets every sync token: each answers 410 from now on.
func (g *FakeGoogle) ExpireSyncTokens() {
	g.Configure(func(g *FakeGoogle) { g.cal.syncs = map[string]syncPoint{} })
}

// cal is owner's calendar id, made when it is the primary one. g.mu is held.
func (g *FakeGoogle) calendarOf(owner, id string) *calendar {
	if g.cal.calendars == nil {
		g.cal.calendars = map[string]map[string]*calendar{}
	}
	if g.cal.calendars[owner] == nil {
		g.cal.calendars[owner] = map[string]*calendar{}
	}
	c := g.cal.calendars[owner][id]
	if c == nil && id == "primary" {
		c = &calendar{id: "primary", events: map[string]*Event{}}
		g.cal.calendars[owner][id] = c
	}
	return c
}

// nextID is a fresh id: base32hex, as Google's event ids are. g.mu is held.
func (g *FakeGoogle) nextEventID() string {
	g.cal.ids++
	return fmt.Sprintf("%sq%05d", strings.ToLower(randomHexText(10)), g.cal.ids)
}

// randomHexText is n random characters of [0-9a-f].
func randomHexText(n int) string {
	t := randomText(n * 2)
	var b strings.Builder
	for _, r := range t {
		b.WriteByte("0123456789abcdef"[int(r)%16])
		if b.Len() == n {
			break
		}
	}
	return b.String()
}

// touch records a change of ev in c. g.mu is held.
func (c *calendar) touch(ev *Event) {
	c.seq++
	ev.updated = c.seq
	ev.Etag = fmt.Sprintf("%q", strconv.FormatInt(3181161784712000+c.seq, 10))
}

// Events are owner's events of calendar id ("" the primary one), in the order
// made, deleted ones included.
func (g *FakeGoogle) Events(owner, id string) []Event {
	if id == "" {
		id = "primary"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.calendarOf(owner, id)
	if c == nil {
		return nil
	}
	out := []Event{}
	for _, eid := range c.order {
		ev := *c.events[eid]
		ev.Private = cloneMap(ev.Private)
		out = append(out, ev)
	}
	return out
}

// LiveEvents are Events without the deleted ones.
func (g *FakeGoogle) LiveEvents(owner, id string) []Event {
	var out []Event
	for _, ev := range g.Events(owner, id) {
		if ev.Status != "cancelled" {
			out = append(out, ev)
		}
	}
	return out
}

// PutEvent is the person adding ev to their calendar id ("" the primary
// one) themselves: its id drawn when empty. It answers the event as kept.
func (g *FakeGoogle) PutEvent(owner, id string, ev Event) Event {
	if id == "" {
		id = "primary"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.calendarOf(owner, id)
	if ev.ID == "" {
		ev.ID = g.nextEventID()
	}
	if ev.Status == "" {
		ev.Status = "confirmed"
	}
	kept := ev
	kept.Private = cloneMap(ev.Private)
	if _, ok := c.events[kept.ID]; !ok {
		c.order = append(c.order, kept.ID)
	}
	c.events[kept.ID] = &kept
	c.touch(&kept)
	return kept
}

// EditEvent is the person editing an event of their calendar id themselves.
func (g *FakeGoogle) EditEvent(owner, id, eventID string, fn func(*Event)) {
	if id == "" {
		id = "primary"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.calendarOf(owner, id)
	ev := c.events[eventID]
	fn(ev)
	c.touch(ev)
}

// Calendars are owner's secondary calendars, by id.
func (g *FakeGoogle) Calendars(owner string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	for id := range g.cal.calendars[owner] {
		if id != "primary" {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// DeleteCalendarAsPerson is the person deleting a secondary calendar.
func (g *FakeGoogle) DeleteCalendarAsPerson(owner, id string) {
	g.Configure(func(g *FakeGoogle) { delete(g.cal.calendars[owner], id) })
}

// Channels are the channels open and not yet expired, by id order.
func (g *FakeGoogle) Channels() []Channel {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := []Channel{}
	for _, c := range g.cal.channels {
		if g.now().Before(c.Expires) {
			out = append(out, *c)
		}
	}
	slices.SortFunc(out, func(a, b Channel) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Stopped are the channel ids channels.stop closed, in order.
func (g *FakeGoogle) Stopped() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.cal.stopped)
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// calendarCall answers one Calendar v3 call. g.mu is held.
func (g *FakeGoogle) calendarCall(req *http.Request, body []byte) answer {
	tok, ok := g.bearer(req)
	if !ok || g.cal.refused[tok.subject] {
		return answer{status: http.StatusUnauthorized, body: g.apiError(http.StatusUnauthorized, "authError")}
	}
	if status, reason := g.failure(req); status > 0 {
		return answer{status: status, body: g.apiError(status, reason)}
	}
	owner := tok.subject
	has := func(scope string) bool { return slices.Contains(tok.scopes, scope) }
	rest := strings.TrimPrefix(req.URL.EscapedPath(), "/calendar/v3/")
	parts := strings.Split(rest, "/")
	for i, p := range parts {
		if u, err := url.PathUnescape(p); err == nil {
			parts[i] = u
		}
	}
	q := req.URL.Query()
	denied := answer{status: http.StatusForbidden, body: g.apiError(http.StatusForbidden, "insufficientPermissions")}
	switch {
	case len(parts) == 2 && parts[0] == "channels" && parts[1] == "stop" && req.Method == http.MethodPost:
		return g.stopChannel(owner, body)
	case len(parts) == 1 && parts[0] == "calendars" && req.Method == http.MethodPost:
		if !has(ScopeCalendarAppCreated) || g.cal.denyCalendar {
			return denied
		}
		return g.insertCalendar(owner, body)
	case len(parts) == 2 && parts[0] == "calendars":
		if !has(ScopeCalendarAppCreated) {
			return denied
		}
		return g.calendarItself(owner, parts[1], req.Method)
	case len(parts) >= 3 && parts[0] == "calendars" && parts[2] == "events":
		calID := parts[1]
		if (calID == "primary" && !has(ScopeCalendarEvents)) || (calID != "primary" && !has(ScopeCalendarEvents) && !has(ScopeCalendarAppCreated)) {
			return denied
		}
		c := g.calendarOf(owner, calID)
		if c == nil {
			return g.notFound()
		}
		switch {
		case len(parts) == 3 && req.Method == http.MethodGet:
			return g.listEvents(owner, c, q)
		case len(parts) == 3 && req.Method == http.MethodPost:
			return g.insertEvent(c, body)
		case len(parts) == 4 && parts[3] == "watch" && req.Method == http.MethodPost && calID == "primary":
			return g.watch(owner, body)
		case len(parts) == 4:
			return g.eventItself(c, parts[3], req.Method, body)
		}
	}
	return g.notFound()
}

func (g *FakeGoogle) gone() answer {
	return answer{status: http.StatusGone, body: g.apiError(http.StatusGone, "deleted")}
}

func (g *FakeGoogle) badRequest() answer {
	return answer{status: http.StatusBadRequest, body: g.apiError(http.StatusBadRequest, "invalid")}
}

func (g *FakeGoogle) insertCalendar(owner string, body []byte) answer {
	var in struct {
		Summary string `json:"summary"`
	}
	if json.Unmarshal(body, &in) != nil || in.Summary == "" {
		return g.badRequest()
	}
	g.calendarOf(owner, "primary")
	id := g.nextEventID() + "@group.calendar.google.com"
	g.cal.calendars[owner][id] = &calendar{id: id, summary: in.Summary, events: map[string]*Event{}}
	return answer{status: http.StatusOK, body: map[string]any{"kind": "calendar#calendar", "id": id, "summary": in.Summary}}
}

func (g *FakeGoogle) calendarItself(owner, id, method string) answer {
	c := g.calendarOf(owner, id)
	if c == nil || id == "primary" {
		return g.notFound()
	}
	switch method {
	case http.MethodGet:
		return answer{status: http.StatusOK, body: map[string]any{"kind": "calendar#calendar", "id": c.id, "summary": c.summary}}
	case http.MethodDelete:
		delete(g.cal.calendars[owner], id)
		return answer{status: http.StatusNoContent}
	}
	return g.notFound()
}

// eventJSON is ev as the API answers it.
func eventJSON(ev *Event) map[string]any {
	out := map[string]any{"kind": "calendar#event", "id": ev.ID, "etag": ev.Etag, "status": ev.Status}
	if ev.Status == "cancelled" {
		return out
	}
	out["summary"] = ev.Summary
	if ev.Description != "" {
		out["description"] = ev.Description
	}
	if ev.Location != "" {
		out["location"] = ev.Location
	}
	out["start"], out["end"] = ev.Start, ev.End
	if ev.Transparency != "" {
		out["transparency"] = ev.Transparency
	}
	out["eventType"] = "default"
	if ev.EventType != "" {
		out["eventType"] = ev.EventType
	}
	if ev.Visibility != "" {
		out["visibility"] = ev.Visibility
	}
	if ev.RecurringEventID != "" {
		out["recurringEventId"] = ev.RecurringEventID
	}
	if len(ev.Attendees) > 0 {
		out["attendees"] = ev.Attendees
	}
	if len(ev.Private) > 0 {
		out["extendedProperties"] = map[string]any{"private": ev.Private}
	}
	return out
}

// applyBody writes a create's or a patch's fields onto ev; false for a body
// that does not read or leaves the event without times.
func applyBody(ev *Event, body []byte) bool {
	var in map[string]json.RawMessage
	if json.Unmarshal(body, &in) != nil {
		return false
	}
	str := func(key string, into *string) bool {
		raw, ok := in[key]
		if !ok {
			return true
		}
		var v *string
		if json.Unmarshal(raw, &v) != nil {
			return false
		}
		*into = ""
		if v != nil {
			*into = *v
		}
		return true
	}
	when := func(key string, into *EventTime) bool {
		raw, ok := in[key]
		if !ok {
			return true
		}
		var v struct {
			Date     *string `json:"date"`
			DateTime *string `json:"dateTime"`
			TimeZone *string `json:"timeZone"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return false
		}
		t := EventTime{}
		if v.Date != nil {
			t.Date = *v.Date
		}
		if v.DateTime != nil {
			t.DateTime = *v.DateTime
		}
		if v.TimeZone != nil {
			t.TimeZone = *v.TimeZone
		}
		*into = t
		return true
	}
	ok := str("summary", &ev.Summary) && str("description", &ev.Description) && str("location", &ev.Location) &&
		str("transparency", &ev.Transparency) && str("visibility", &ev.Visibility) &&
		when("start", &ev.Start) && when("end", &ev.End)
	if !ok {
		return false
	}
	if raw, has := in["extendedProperties"]; has {
		var v struct {
			Private map[string]string `json:"private"`
		}
		if json.Unmarshal(raw, &v) != nil {
			return false
		}
		if ev.Private == nil {
			ev.Private = map[string]string{}
		}
		for k, val := range v.Private {
			ev.Private[k] = val
		}
	}
	valid := func(t EventTime) bool { return (t.Date == "") != (t.DateTime == "") }
	return valid(ev.Start) && valid(ev.End) && (ev.Start.Date == "") == (ev.End.Date == "")
}

func (g *FakeGoogle) insertEvent(c *calendar, body []byte) answer {
	ev := &Event{ID: g.nextEventID(), Status: "confirmed"}
	if !applyBody(ev, body) {
		return g.badRequest()
	}
	c.events[ev.ID] = ev
	c.order = append(c.order, ev.ID)
	c.touch(ev)
	return answer{status: http.StatusOK, body: eventJSON(ev)}
}

func (g *FakeGoogle) eventItself(c *calendar, id, method string, body []byte) answer {
	ev := c.events[id]
	if ev == nil {
		return g.notFound()
	}
	switch method {
	case http.MethodGet:
		return answer{status: http.StatusOK, body: eventJSON(ev)}
	case http.MethodPatch:
		if ev.Status == "cancelled" {
			return g.gone()
		}
		edited := *ev
		edited.Private = cloneMap(ev.Private)
		if !applyBody(&edited, body) {
			return g.badRequest()
		}
		*ev = edited
		c.touch(ev)
		return answer{status: http.StatusOK, body: eventJSON(ev)}
	case http.MethodDelete:
		if ev.Status == "cancelled" {
			return g.gone()
		}
		ev.Status = "cancelled"
		c.touch(ev)
		return answer{status: http.StatusNoContent}
	}
	return g.notFound()
}

// instant is an event time's instant: a date at midnight UTC.
func instant(t EventTime) time.Time {
	if t.Date != "" {
		d, _ := time.Parse(time.DateOnly, t.Date)
		return d
	}
	d, _ := time.Parse(time.RFC3339, t.DateTime)
	return d
}

func (g *FakeGoogle) listEvents(owner string, c *calendar, q url.Values) answer {
	var events []*Event
	since := int64(-1)
	if token := q.Get("syncToken"); token != "" {
		p, ok := g.cal.syncs[token]
		if !ok || p.owner != owner || p.calendar != c.id {
			return answer{status: http.StatusGone, body: g.apiError(http.StatusGone, "fullSyncRequired")}
		}
		if q.Has("timeMin") || q.Has("timeMax") || q.Has("privateExtendedProperty") {
			return g.badRequest()
		}
		since = p.seq
	}
	var from, to time.Time
	if v := q.Get("timeMin"); v != "" {
		from, _ = time.Parse(time.RFC3339, v)
	}
	if v := q.Get("timeMax"); v != "" {
		to, _ = time.Parse(time.RFC3339, v)
	}
	prop := q.Get("privateExtendedProperty")
	for _, id := range c.order {
		ev := c.events[id]
		switch {
		case since >= 0:
			if ev.updated <= since {
				continue
			}
		default:
			if ev.Status == "cancelled" && q.Get("showDeleted") != "true" {
				continue
			}
			if !from.IsZero() && !instant(ev.End).After(from) {
				continue
			}
			if !to.IsZero() && !instant(ev.Start).Before(to) {
				continue
			}
		}
		if prop != "" {
			k, v, _ := strings.Cut(prop, "=")
			if ev.Private[k] != v || ev.Status == "cancelled" {
				continue
			}
		}
		events = append(events, ev)
	}
	size := 250
	if n, err := strconv.Atoi(q.Get("maxResults")); err == nil && n > 0 {
		size = n
	}
	offset := 0
	if p := q.Get("pageToken"); p != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(p, "o"))
		if err != nil || !strings.HasPrefix(p, "o") || n > len(events) {
			return g.badRequest()
		}
		offset = n
	}
	end := min(offset+size, len(events))
	items := []map[string]any{}
	for _, ev := range events[offset:end] {
		items = append(items, eventJSON(ev))
	}
	out := map[string]any{"kind": "calendar#events", "items": items}
	if end < len(events) {
		out["nextPageToken"] = "o" + strconv.Itoa(end)
	} else if prop == "" && (since >= 0 || !g.cal.noSyncToken) {
		token := "sync-" + randomText(24)
		if g.cal.syncs == nil {
			g.cal.syncs = map[string]syncPoint{}
		}
		g.cal.syncs[token] = syncPoint{owner: owner, calendar: c.id, seq: c.seq}
		out["nextSyncToken"] = token
	}
	return answer{status: http.StatusOK, body: out}
}

func (g *FakeGoogle) watch(owner string, body []byte) answer {
	var in struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	if json.Unmarshal(body, &in) != nil || in.ID == "" || in.Type != "web_hook" || !strings.HasPrefix(in.Address, "https://") {
		return g.badRequest()
	}
	life := g.cal.channelLife
	if life == 0 {
		life = 7 * 24 * time.Hour
	}
	ch := &Channel{ID: in.ID, Token: in.Token, Address: in.Address, ResourceID: "res-" + owner, Owner: owner, Expires: g.now().Add(life)}
	if g.cal.channels == nil {
		g.cal.channels = map[string]*Channel{}
	}
	g.cal.channels[in.ID] = ch
	return answer{status: http.StatusOK, body: map[string]any{
		"kind": "api#channel", "id": ch.ID, "resourceId": ch.ResourceID,
		"resourceUri": "https://www.googleapis.com/calendar/v3/calendars/primary/events?alt=json",
		"expiration":  strconv.FormatInt(ch.Expires.UnixMilli(), 10),
	}}
}

func (g *FakeGoogle) stopChannel(owner string, body []byte) answer {
	var in struct {
		ID         string `json:"id"`
		ResourceID string `json:"resourceId"`
	}
	if json.Unmarshal(body, &in) != nil {
		return g.badRequest()
	}
	ch := g.cal.channels[in.ID]
	if ch == nil || ch.ResourceID != in.ResourceID || ch.Owner != owner {
		return g.notFound()
	}
	delete(g.cal.channels, in.ID)
	g.cal.stopped = append(g.cal.stopped, in.ID)
	return answer{status: http.StatusNoContent}
}

// calendarBatch answers the batch endpoint: each part read as an HTTP request
// under the batch's token, answered by calendarCall, and recorded as a call
// of its own. g.mu is held.
func (g *FakeGoogle) calendarBatch(req *http.Request, body []byte) answer {
	media, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || media != "multipart/mixed" {
		return g.badRequest()
	}
	r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var out bytes.Buffer
	w := multipart.NewWriter(&out)
	for {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return g.badRequest()
		}
		id := strings.TrimSuffix(strings.TrimPrefix(part.Header.Get("Content-ID"), "<"), ">")
		inner, err := http.ReadRequest(bufio.NewReader(part))
		if err != nil {
			return g.badRequest()
		}
		innerBody, _ := io.ReadAll(inner.Body)
		u, err := url.Parse("https://" + APIsHost + inner.RequestURI)
		if err != nil {
			return g.badRequest()
		}
		inner.URL = u
		inner.Host = APIsHost
		inner.Header.Set("Authorization", req.Header.Get("Authorization"))
		var a answer
		if g.cal.throttle > 0 {
			g.cal.throttle--
			a = answer{status: http.StatusForbidden, body: g.apiError(http.StatusForbidden, "rateLimitExceeded")}
		} else {
			a = g.calendarCall(inner, innerBody)
		}
		g.calls = append(g.calls, Call{Host: APIsHost, Method: inner.Method, Path: u.Path, Query: u.RawQuery, Body: string(innerBody), Status: a.status})
		pw, _ := w.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/http"}, "Content-ID": {"<response-" + id + ">"}})
		var b []byte
		if a.body != nil {
			b, _ = json.Marshal(a.body)
		}
		_, _ = fmt.Fprintf(pw, "HTTP/1.1 %d %s\r\nContent-Type: application/json; charset=UTF-8\r\nContent-Length: %d\r\n\r\n", a.status, http.StatusText(a.status), len(b))
		_, _ = pw.Write(b)
	}
	_ = w.Close()
	return answer{status: http.StatusOK, header: http.Header{"Content-Type": {"multipart/mixed; boundary=" + w.Boundary()}}, raw: out.Bytes()}
}
