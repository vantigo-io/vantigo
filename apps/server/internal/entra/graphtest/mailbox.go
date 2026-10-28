package graphtest

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// This file is the users' mailboxes (calendar Outlook sync, plan reading
// 12): calendars made, read and deleted under /users/{id}/calendars; events
// made in the default calendar (/users/{id}/events) or a secondary one
// (/users/{id}/calendars/{id}/events), patched and deleted by
// /users/{id}/events/{id}, and listed in pages of $top (10 when absent,
// Graph's default) with an @odata.nextLink. A single-value extended
// property — an event's or a calendar's — is kept as it was written, and
// answered only when $expand asks for it by id; events are filtered on with
// Graph's lambda — singleValueExtendedProperties/Any(ep: ep/id eq '…' and
// startswith(ep/value, '…')), optionally followed by and start/dateTime ge
// '…' (compared as the stored dateTime's text, whatever its zone) — the one
// filter the events take, and calendars on name eq '…'. A user marked MailboxDenied answers 403 ErrorAccessDenied
// (outside an RBAC for Applications scope), NoMailbox 404
// MailboxNotEnabledForRESTAPI; a calendar or an event that is not there is
// 404 ErrorItemNotFound; a second calendar of a name is 409
// ErrorFolderExists. ThrottleBatch makes requests inside a $batch answer
// 429 with Retry-After, one by one.
//
// For the write-back's reads (calendar Outlook sync, delivery C's plan
// reading 10): an event is read by id through its calendar's own path too —
// /users/{id}/calendar/events/{id} for the default calendar,
// /users/{id}/calendars/{id}/events/{id} for another — 404 for an event of
// another calendar; a secondary calendar answers its calendarView
// (startDateTime and endDateTime required, every event overlapping them, in
// pages of $top with an @odata.nextLink); a $select naming fields narrows an
// event to them and its id; an event read by id honours Prefer:
// outlook.timezone="UTC" (its times in UTC, Graph's seven fractional
// digits) and outlook.body-content-type="text" (an HTML body answered as
// text, its tags stripped) unless HTMLBodies is set; and an event's
// attendees are answered as Graph's, with their addresses. EditEvent
// (mailbox_delta.go) is the person's edit: the change key moves.

// Calendar is a calendar in a user's mailbox.
type Calendar struct {
	ID      string
	Name    string
	Default bool
	// Properties are the calendar's single-value extended properties by id.
	Properties map[string]string
}

// DateTimeZone is an event's start or end as Graph takes it.
type DateTimeZone struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

// Event is an event in a user's mailbox, as Events answers it.
type Event struct {
	ID           string
	CalendarID   string
	ChangeKey    string
	Subject      string
	BodyType     string
	Body         string
	Location     string
	IsAllDay     bool
	Start, End   DateTimeZone
	ShowAs       string
	Sensitivity  string
	IsReminderOn bool
	// Type is singleInstance ("" is one), occurrence, exception or
	// seriesMaster.
	Type        string
	IsCancelled bool
	// Attendees are the attendees' addresses: a meeting has some.
	Attendees []string
	// Properties are the event's single-value extended properties by id.
	Properties map[string]string
	// LastWrite is the body of the create or the PATCH that last wrote the
	// event, as it was sent.
	LastWrite map[string]json.RawMessage

	// seq is the fake's serial at the event's last change, its place in
	// the mailbox's change log (mailbox_delta.go).
	seq int
}

// mailbox is one user's calendars and events, and the delta's state: the
// events deleted, by the serial of their deletion, and the refusal a delta
// link is answered while ExpireDelta holds.
type mailbox struct {
	calendars    []*Calendar
	events       []*Event
	removed      []tombstone
	deltaExpired int
}

// ThrottleBatch makes the next n requests inside a $batch answer 429
// ApplicationThrottled with Retry-After: 1, each on its own.
func (g *FakeGraph) ThrottleBatch(n int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.batchThrottle += n
}

// ThrottleBatchFor is ThrottleBatch with a Retry-After of seconds, answered
// in the $batch as a JSON number rather than a string; seconds 0 goes back
// to ThrottleBatch's string "1".
func (g *FakeGraph) ThrottleBatchFor(n, seconds int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.batchThrottle += n
	g.batchWait = seconds
}

// Calendars is the user's calendars, the default one first.
func (g *FakeGraph) Calendars(userID string) []Calendar {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Calendar
	for _, c := range g.mailboxOf(userID).calendars {
		out = append(out, copyCalendar(c))
	}
	return out
}

// AddCalendar seeds a secondary calendar named name in the user's mailbox,
// carrying properties (id → value) when any are given, and answers it.
func (g *FakeGraph) AddCalendar(userID, name string, properties ...map[string]string) Calendar {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := &Calendar{ID: g.itemID(), Name: name}
	for _, props := range properties {
		for k, v := range props {
			if c.Properties == nil {
				c.Properties = map[string]string{}
			}
			c.Properties[k] = v
		}
	}
	mb := g.mailboxOf(userID)
	mb.calendars = append(mb.calendars, c)
	return copyCalendar(c)
}

// Events is the user's events in the calendar calendarID — "" the default
// one — in the order they were made.
func (g *FakeGraph) Events(userID, calendarID string) []Event {
	g.mu.Lock()
	defer g.mu.Unlock()
	mb := g.mailboxOf(userID)
	cal := mb.calendar(calendarID)
	var out []Event
	for _, e := range mb.events {
		if cal != nil && e.CalendarID == cal.ID {
			out = append(out, copyEvent(e))
		}
	}
	return out
}

// AddEvent seeds an event in the user's calendar calendarID ("" the default
// one) — one a person made in Outlook, or one carrying a marker — and
// answers it with its id.
func (g *FakeGraph) AddEvent(userID, calendarID string, e Event) Event {
	g.mu.Lock()
	defer g.mu.Unlock()
	mb := g.mailboxOf(userID)
	cal := mb.calendar(calendarID)
	if cal == nil {
		cal = mb.calendars[0]
	}
	stored := copyEvent(&e)
	if stored.ID == "" {
		stored.ID = g.itemID()
	}
	stored.CalendarID, stored.ChangeKey = cal.ID, g.changeKey()
	stored.seq = g.serial
	mb.events = append(mb.events, &stored)
	return copyEvent(&stored)
}

// mailboxOf is the user's mailbox, made with its default calendar on first
// use.
func (g *FakeGraph) mailboxOf(userID string) *mailbox {
	key := strings.ToLower(userID)
	if g.mailboxes == nil {
		g.mailboxes = map[string]*mailbox{}
	}
	mb, ok := g.mailboxes[key]
	if !ok {
		mb = &mailbox{calendars: []*Calendar{{ID: g.itemID(), Name: "Calendar", Default: true}}}
		g.mailboxes[key] = mb
	}
	return mb
}

// calendar is the calendar id, the default one for "", nil when there is
// none.
func (mb *mailbox) calendar(id string) *Calendar {
	for _, c := range mb.calendars {
		if (id == "" && c.Default) || (id != "" && c.ID == id) {
			return c
		}
	}
	return nil
}

// event is the event id, nil when there is none.
func (mb *mailbox) event(id string) *Event {
	for _, e := range mb.events {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// itemID is a fresh Exchange-like id: base64url, padded, as Graph's are.
func (g *FakeGraph) itemID() string {
	id := uuid.New()
	return "AAMkAG" + base64.URLEncoding.EncodeToString(id[:])
}

// changeKey is a fresh change key.
func (g *FakeGraph) changeKey() string {
	g.serial++
	return "CQAAAB" + strconv.Itoa(g.serial)
}

func itemNotFound() answer {
	return graphError(http.StatusNotFound, "ErrorItemNotFound", "The specified object was not found in the store.")
}

// mailboxRoute answers /users/{id}/calendars… and /users/{id}/events… for u,
// seg being the path from "calendars" or "events" on.
func (g *FakeGraph) mailboxRoute(u *User, method string, seg []string, query url.Values, body []byte) answer {
	if a, refused := mailboxRefused(u); refused {
		return a
	}
	mb := g.mailboxOf(u.ID)
	if seg[0] == "calendar" {
		// The default calendar's own path: an event read by id.
		if len(seg) == 3 && seg[1] == "events" && method == http.MethodGet {
			return g.getEventIn(mb, mb.calendar(""), seg[2], query)
		}
		return graphError(http.StatusBadRequest, "BadRequest", "Unsupported request: "+method+" users/{id}/"+strings.Join(seg, "/")+".")
	}
	if seg[0] == "events" {
		switch {
		case len(seg) == 1 && method == http.MethodGet:
			return g.listEvents(u, mb, mb.calendar(""), query)
		case len(seg) == 1 && method == http.MethodPost:
			return g.createEvent(mb, mb.calendar(""), body)
		case len(seg) == 2:
			e := mb.event(seg[1])
			if e == nil {
				return itemNotFound()
			}
			switch method {
			case http.MethodGet:
				if x := query.Get("$expand"); x != "" && !markerExpand.MatchString(x) {
					return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The $expand is not valid.")
				}
				return answer{status: http.StatusOK, body: eventJSON(e, query)}
			case http.MethodPatch:
				return g.patchEvent(e, body)
			case http.MethodDelete:
				g.removeEvent(mb, e)
				return answer{status: http.StatusNoContent}
			}
		}
		return methodNotAllowed()
	}
	if len(seg) == 1 {
		switch method {
		case http.MethodGet:
			return listCalendars(mb, query)
		case http.MethodPost:
			return g.createCalendar(mb, body)
		}
		return methodNotAllowed()
	}
	cal := mb.calendar(seg[1])
	if cal == nil {
		return itemNotFound()
	}
	switch {
	case len(seg) == 2 && method == http.MethodGet:
		if x := query.Get("$expand"); x != "" && !markerExpand.MatchString(x) {
			return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The $expand is not valid.")
		}
		return answer{status: http.StatusOK, body: calendarJSON(cal, query)}
	case len(seg) == 2 && method == http.MethodDelete:
		if cal.Default {
			return graphError(http.StatusBadRequest, "ErrorInvalidRequest", "The default calendar cannot be deleted.")
		}
		mb.calendars = slices.DeleteFunc(mb.calendars, func(c *Calendar) bool { return c == cal })
		mb.events = slices.DeleteFunc(mb.events, func(e *Event) bool { return e.CalendarID == cal.ID })
		return answer{status: http.StatusNoContent}
	case len(seg) == 3 && seg[2] == "events" && method == http.MethodGet:
		return g.listEvents(u, mb, cal, query)
	case len(seg) == 3 && seg[2] == "events" && method == http.MethodPost:
		return g.createEvent(mb, cal, body)
	case len(seg) == 4 && seg[2] == "events" && method == http.MethodGet:
		return g.getEventIn(mb, cal, seg[3], query)
	case len(seg) == 3 && seg[2] == "calendarView" && method == http.MethodGet:
		return g.calendarView(u, mb, cal, query)
	}
	return graphError(http.StatusBadRequest, "BadRequest", "Unsupported request: "+method+" users/{id}/"+strings.Join(seg, "/")+".")
}

// mailboxRefused is the refusal of every request to u's mailbox: 403 when
// it is outside the application's scope, 404 when u has none.
func mailboxRefused(u *User) (answer, bool) {
	switch {
	case u.MailboxDenied:
		return graphError(http.StatusForbidden, "ErrorAccessDenied", "Access is denied. Check credentials and try again."), true
	case u.NoMailbox:
		return graphError(http.StatusNotFound, "MailboxNotEnabledForRESTAPI", "The mailbox is either inactive, soft-deleted, or is hosted on-premise."), true
	}
	return answer{}, false
}

// calendarJSON is c as Graph answers it, with the property $expand names
// when the calendar has it.
func calendarJSON(c *Calendar, query url.Values) map[string]any {
	out := map[string]any{"id": c.ID, "name": c.Name, "isDefaultCalendar": c.Default, "canEdit": true}
	if m := markerExpand.FindStringSubmatch(query.Get("$expand")); m != nil {
		if v, ok := c.Properties[m[1]]; ok {
			out["singleValueExtendedProperties"] = []any{map[string]any{"id": m[1], "value": v}}
		}
	}
	return out
}

// calendarNameFilter is the one $filter the calendars' list takes.
var calendarNameFilter = regexp.MustCompile(`^name eq '((?:[^']|'')*)'$`)

func listCalendars(mb *mailbox, query url.Values) answer {
	name, filtered := "", false
	if f := query.Get("$filter"); f != "" {
		m := calendarNameFilter.FindStringSubmatch(f)
		if m == nil {
			return graphError(http.StatusBadRequest, "ErrorInvalidUrlQueryFilter", "The query filter contains one or more invalid nodes.")
		}
		name, filtered = strings.ReplaceAll(m[1], "''", "'"), true
	}
	if x := query.Get("$expand"); x != "" && !markerExpand.MatchString(x) {
		return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The $expand is not valid.")
	}
	out := []any{}
	for _, c := range mb.calendars {
		if !filtered || strings.EqualFold(c.Name, name) {
			out = append(out, calendarJSON(c, query))
		}
	}
	return answer{status: http.StatusOK, body: map[string]any{"value": out}}
}

func (g *FakeGraph) createCalendar(mb *mailbox, body []byte) answer {
	var in struct {
		Name       string `json:"name"`
		Properties []struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		} `json:"singleValueExtendedProperties"`
	}
	if err := json.Unmarshal(body, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		return graphError(http.StatusBadRequest, "ErrorInvalidRequest", "A calendar needs a name.")
	}
	var props map[string]string
	for _, p := range in.Properties {
		if !propertyID.MatchString(p.ID) {
			return graphError(http.StatusBadRequest, "ErrorInvalidProperty", "The extended property's id is not valid.")
		}
		if props == nil {
			props = map[string]string{}
		}
		props[p.ID] = p.Value
	}
	for _, c := range mb.calendars {
		if strings.EqualFold(c.Name, in.Name) {
			return graphError(http.StatusConflict, "ErrorFolderExists", "A folder with the specified name already exists.")
		}
	}
	c := &Calendar{ID: g.itemID(), Name: in.Name, Properties: props}
	mb.calendars = append(mb.calendars, c)
	return answer{status: http.StatusCreated, body: calendarJSON(c, nil)}
}

// The values Graph takes for an event's enumerations.
var (
	showAsValues      = []string{"free", "tentative", "busy", "oof", "workingElsewhere", "unknown"}
	sensitivityValues = []string{"normal", "personal", "private", "confidential"}
	propertyID        = regexp.MustCompile(`^String \{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\} Name \S+$`)
	midnight          = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T00:00:00(\.0+)?$`)
)

func (g *FakeGraph) createEvent(mb *mailbox, cal *Calendar, body []byte) answer {
	e := &Event{CalendarID: cal.ID, ShowAs: "busy", Sensitivity: "normal", IsReminderOn: true, BodyType: "html"}
	if a, ok := g.writeEvent(e, body, true); !ok {
		return a
	}
	e.ID = g.itemID()
	mb.events = append(mb.events, e)
	return answer{status: http.StatusCreated, body: eventJSON(e, nil)}
}

func (g *FakeGraph) patchEvent(e *Event, body []byte) answer {
	next := copyEvent(e)
	if a, ok := g.writeEvent(&next, body, false); !ok {
		return a
	}
	*e = next
	return answer{status: http.StatusOK, body: eventJSON(e, nil)}
}

// writeEvent applies body's properties to e — every one on a create, the
// ones present on a PATCH — and judges the result as Graph does: a start
// and an end in one zone, the end not before the start, an all-day event's
// at midnight; the enumerations' values; a property's id in the
// String {GUID} Name form. The answer is the refusal when it is false.
func (g *FakeGraph) writeEvent(e *Event, body []byte, create bool) (answer, bool) {
	bad := func(code, message string) (answer, bool) {
		return graphError(http.StatusBadRequest, code, message), false
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal(body, &props); err != nil {
		return bad("ErrorInvalidRequest", "Cannot read the request body.")
	}
	var in struct {
		Subject *string `json:"subject"`
		Body    *struct {
			ContentType string `json:"contentType"`
			Content     string `json:"content"`
		} `json:"body"`
		Location *struct {
			DisplayName string `json:"displayName"`
		} `json:"location"`
		IsAllDay     *bool         `json:"isAllDay"`
		Start        *DateTimeZone `json:"start"`
		End          *DateTimeZone `json:"end"`
		ShowAs       *string       `json:"showAs"`
		Sensitivity  *string       `json:"sensitivity"`
		IsReminderOn *bool         `json:"isReminderOn"`
		Properties   []struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		} `json:"singleValueExtendedProperties"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return bad("ErrorInvalidRequest", "Cannot read the request body.")
	}
	if create && (in.Start == nil || in.End == nil) {
		return bad("ErrorInvalidRequest", "An event needs a start and an end.")
	}
	if in.Subject != nil {
		e.Subject = *in.Subject
	}
	if in.Body != nil {
		e.BodyType, e.Body = strings.ToLower(in.Body.ContentType), in.Body.Content
	}
	if in.Location != nil {
		e.Location = in.Location.DisplayName
	}
	if in.IsAllDay != nil {
		e.IsAllDay = *in.IsAllDay
	}
	if in.Start != nil {
		e.Start = *in.Start
	}
	if in.End != nil {
		e.End = *in.End
	}
	if in.ShowAs != nil {
		if !slices.Contains(showAsValues, *in.ShowAs) {
			return bad("ErrorInvalidRequest", "Invalid showAs.")
		}
		e.ShowAs = *in.ShowAs
	}
	if in.Sensitivity != nil {
		if !slices.Contains(sensitivityValues, *in.Sensitivity) {
			return bad("ErrorInvalidRequest", "Invalid sensitivity.")
		}
		e.Sensitivity = *in.Sensitivity
	}
	if in.IsReminderOn != nil {
		e.IsReminderOn = *in.IsReminderOn
	}
	if e.Start.TimeZone == "" || e.End.TimeZone == "" || e.Start.DateTime == "" || e.End.DateTime == "" {
		return bad("ErrorInvalidRequest", "An event's start and end need a dateTime and a timeZone.")
	}
	if e.End.DateTime < e.Start.DateTime && e.Start.TimeZone == e.End.TimeZone {
		return bad("ErrorInvalidRequest", "The end of an event is before its start.")
	}
	if e.IsAllDay && (e.Start.TimeZone != e.End.TimeZone || !midnight.MatchString(e.Start.DateTime) ||
		!midnight.MatchString(e.End.DateTime) || e.End.DateTime <= e.Start.DateTime) {
		return bad("ErrorInvalidRequest", "An all-day event's start and end must be at midnight in one time zone, the end after the start.")
	}
	for _, p := range in.Properties {
		if !propertyID.MatchString(p.ID) {
			return bad("ErrorInvalidProperty", "The extended property's id is not valid.")
		}
		if e.Properties == nil {
			e.Properties = map[string]string{}
		}
		e.Properties[p.ID] = p.Value
	}
	e.LastWrite = props
	e.ChangeKey = g.changeKey()
	e.seq = g.serial
	return answer{}, true
}

// The $filter and $expand the events' list takes.
var (
	markerFilter = regexp.MustCompile(`^singleValueExtendedProperties/Any\(ep: ep/id eq '([^']+)' and startswith\(ep/value, '([^']*)'\)\)(?: and start/dateTime ge '(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})')?$`)
	markerExpand = regexp.MustCompile(`^singleValueExtendedProperties\(\$filter=id eq '([^']+)'\)$`)
)

// listEvents is a calendar's events in pages of $top: filtered on a marker
// when $filter asks, each with the property $expand names, $select=id
// narrowing an event to its id.
func (g *FakeGraph) listEvents(u *User, mb *mailbox, cal *Calendar, query url.Values) answer {
	top := 10
	if v := query.Get("$top"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The value of $top is not valid.")
		}
		top = n
	}
	skip := 0
	if v := query.Get("$skiptoken"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "Invalid skip token.")
		}
		skip = n
	}
	var filterID, filterPrefix, filterFrom string
	if f := query.Get("$filter"); f != "" {
		m := markerFilter.FindStringSubmatch(f)
		if m == nil {
			return graphError(http.StatusBadRequest, "ErrorInvalidUrlQueryFilter", "The query filter contains one or more invalid nodes.")
		}
		filterID, filterPrefix, filterFrom = m[1], m[2], m[3]
	}
	if x := query.Get("$expand"); x != "" && !markerExpand.MatchString(x) {
		return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The $expand is not valid.")
	}
	var all []*Event
	for _, e := range mb.events {
		if e.CalendarID != cal.ID {
			continue
		}
		if filterID != "" {
			v, ok := e.Properties[filterID]
			if !ok || !strings.HasPrefix(v, filterPrefix) {
				continue
			}
		}
		if filterFrom != "" && e.Start.DateTime < filterFrom {
			continue
		}
		all = append(all, e)
	}
	out := []any{}
	for i := skip; i < len(all) && i < skip+top; i++ {
		out = append(out, eventJSON(all[i], query))
	}
	body := map[string]any{"value": out}
	if skip+top < len(all) {
		next := url.Values{}
		for k, v := range query {
			next[k] = slices.Clone(v)
		}
		next.Set("$top", strconv.Itoa(top))
		next.Set("$skiptoken", strconv.Itoa(skip+top))
		path := "/users/" + u.ID + "/events"
		if !cal.Default {
			path = "/users/" + u.ID + "/calendars/" + url.PathEscape(cal.ID) + "/events"
		}
		body["@odata.nextLink"] = g.nextLink("https://" + GraphHost + "/v1.0" + path + "?" + strings.ReplaceAll(next.Encode(), "+", "%20"))
	}
	return answer{status: http.StatusOK, body: body}
}

// eventJSON is e as Graph answers it: every property, or under $select the
// id and the properties it names; with the property $expand names when the
// event has it.
func eventJSON(e *Event, query url.Values) map[string]any {
	attendees := []any{}
	for _, a := range e.Attendees {
		attendees = append(attendees, map[string]any{"type": "required",
			"emailAddress": map[string]any{"address": a, "name": a}, "status": map[string]any{"response": "none"}})
	}
	all := map[string]any{
		"changeKey":    e.ChangeKey,
		"subject":      e.Subject,
		"body":         map[string]any{"contentType": e.BodyType, "content": e.Body},
		"location":     map[string]any{"displayName": e.Location},
		"isAllDay":     e.IsAllDay,
		"start":        e.Start,
		"end":          e.End,
		"showAs":       e.ShowAs,
		"sensitivity":  e.Sensitivity,
		"isReminderOn": e.IsReminderOn,
		"type":         cmp.Or(e.Type, "singleInstance"),
		"isCancelled":  e.IsCancelled,
		"attendees":    attendees,
	}
	out := map[string]any{"id": e.ID}
	if sel := query.Get("$select"); sel != "" {
		for _, f := range strings.Split(sel, ",") {
			if v, ok := all[strings.TrimSpace(f)]; ok {
				out[strings.TrimSpace(f)] = v
			}
		}
	} else {
		for k, v := range all {
			out[k] = v
		}
	}
	if m := markerExpand.FindStringSubmatch(query.Get("$expand")); m != nil {
		if v, ok := e.Properties[m[1]]; ok {
			out["singleValueExtendedProperties"] = []any{map[string]any{"id": m[1], "value": v}}
		}
	}
	return out
}

// headerOf is a batch request's header name, whatever its case.
func headerOf(h map[string]string, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func copyCalendar(c *Calendar) Calendar {
	out := *c
	if c.Properties != nil {
		out.Properties = make(map[string]string, len(c.Properties))
		for k, v := range c.Properties {
			out.Properties[k] = v
		}
	}
	return out
}

func copyEvent(e *Event) Event {
	c := *e
	if e.Properties != nil {
		c.Properties = make(map[string]string, len(e.Properties))
		for k, v := range e.Properties {
			c.Properties[k] = v
		}
	}
	if e.LastWrite != nil {
		c.LastWrite = make(map[string]json.RawMessage, len(e.LastWrite))
		for k, v := range e.LastWrite {
			c.LastWrite[k] = slices.Clone(v)
		}
	}
	return c
}

// getEventIn answers GET of the event id through cal's own path: 404 when
// it is not in cal, else the event as eventJSON answers it, under the
// request's Prefer (preferred).
func (g *FakeGraph) getEventIn(mb *mailbox, cal *Calendar, id string, query url.Values) answer {
	e := mb.event(id)
	if e == nil || cal == nil || e.CalendarID != cal.ID {
		return itemNotFound()
	}
	if x := query.Get("$expand"); x != "" && !markerExpand.MatchString(x) {
		return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The $expand is not valid.")
	}
	return answer{status: http.StatusOK, body: g.preferred(e, eventJSON(e, query))}
}

// preferred is out, e's answer, under the request's Prefer: its times in
// UTC with seven fractional digits for outlook.timezone="UTC", and an HTML
// body answered as text, its tags stripped, for
// outlook.body-content-type="text" — unless HTMLBodies is set.
func (g *FakeGraph) preferred(e *Event, out map[string]any) map[string]any {
	if strings.Contains(g.prefer, `outlook.timezone="UTC"`) {
		if _, ok := out["start"]; ok {
			out["start"], out["end"] = deltaDateTime(e.Start, true), deltaDateTime(e.End, true)
		}
	}
	if _, ok := out["body"]; ok && strings.Contains(g.prefer, `outlook.body-content-type="text"`) && !g.HTMLBodies &&
		strings.EqualFold(e.BodyType, "html") {
		out["body"] = map[string]any{"contentType": "text", "content": strings.TrimSpace(htmlTag.ReplaceAllString(e.Body, ""))}
	}
	return out
}

// htmlTag is a tag the fake strips from an HTML body answered as text.
var htmlTag = regexp.MustCompile(`<[^>]*>`)

// calendarView answers GET /users/{id}/calendars/{id}/calendarView: every
// event of cal overlapping startDateTime..endDateTime (400 without them),
// in pages of $top with an @odata.nextLink carrying a $skiptoken.
func (g *FakeGraph) calendarView(u *User, mb *mailbox, cal *Calendar, query url.Values) answer {
	from, err1 := time.Parse(time.RFC3339, query.Get("startDateTime"))
	to, err2 := time.Parse(time.RFC3339, query.Get("endDateTime"))
	if err1 != nil || err2 != nil || !to.After(from) {
		return graphError(http.StatusBadRequest, "ErrorInvalidParameter", "startDateTime and endDateTime are required.")
	}
	if query.Has("$filter") || query.Has("$expand") {
		return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The fake's calendarView takes no $filter or $expand.")
	}
	top := 10
	if v := query.Get("$top"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "The value of $top is not valid.")
		}
		top = n
	}
	skip := 0
	if v := query.Get("$skiptoken"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return graphError(http.StatusBadRequest, "ErrorInvalidUrlQuery", "Invalid skip token.")
		}
		skip = n
	}
	var all []*Event
	for _, e := range mb.events {
		if e.CalendarID == cal.ID && overlaps(e, from, to) {
			all = append(all, e)
		}
	}
	out := []any{}
	for i := skip; i < len(all) && i < skip+top; i++ {
		out = append(out, eventJSON(all[i], query))
	}
	body := map[string]any{"value": out}
	if skip+top < len(all) {
		next := url.Values{}
		for k, v := range query {
			next[k] = slices.Clone(v)
		}
		next.Set("$top", strconv.Itoa(top))
		next.Set("$skiptoken", strconv.Itoa(skip+top))
		body["@odata.nextLink"] = g.nextLink("https://" + GraphHost + "/v1.0/users/" + u.ID + "/calendars/" + url.PathEscape(cal.ID) +
			"/calendarView?" + strings.ReplaceAll(next.Encode(), "+", "%20"))
	}
	return answer{status: http.StatusOK, body: body}
}

// nextLink is link through NextLinks when it is set.
func (g *FakeGraph) nextLink(link string) string {
	if g.NextLinks != nil {
		return g.NextLinks(link)
	}
	return link
}
