package graphtest

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// This file is the default calendar's delta (calendar Outlook sync, delivery
// B's plan reading 12): GET /users/{id}/calendarView/delta, Graph's
// calendarView delta, answered from a change log — every event's last change
// and every deletion carry the fake's serial at the time.
//
// A fresh query names startDateTime and endDateTime (400 without) and
// answers every event of the default calendar overlapping that window; the
// window is then kept in the tokens, as Graph keeps it. A $deltatoken answers
// the changes since it was issued: an event in the window whole, one deleted
// or moved out of it {"id", "@removed": {"reason": "deleted"}}. Pages hold
// Prefer: odata.maxpagesize events (10 without; DeltaPageSize caps it), with
// an @odata.nextLink carrying a $skiptoken until the last, which carries the
// @odata.deltaLink and its $deltatoken. Each event is answered whole —
// subject, body and all — its start and end converted to UTC when Prefer
// asks outlook.timezone="UTC", with Graph's seven fractional digits. A token
// the fake did not issue for the user is 400 syncStateNotFound; ExpireDelta
// makes every token of the user's answer a refusal until a fresh query.

// tombstone is an event deleted from a mailbox: its id, its calendar and
// the serial of its deletion.
type tombstone struct {
	id, calendarID string
	seq            int
}

// deltaState is what a token stands for: the user, the window, the changes
// after since up to upto, and, for a $skiptoken, how many are answered.
type deltaState struct {
	user        string
	from, to    time.Time
	since, upto int
	offset      int
}

// deltaRoute is the path of a user's calendar delta.
var deltaRoute = regexp.MustCompile(`^/users/([^/]+)/calendarView/delta$`)

// deltaPageSize is the most events Prefer's odata.maxpagesize asks for.
var deltaPageSize = regexp.MustCompile(`odata\.maxpagesize=(\d+)`)

// EditEvent changes the user's event id by fn — the person moving it,
// cancelling it, changing its showAs — as a change the delta answers; false
// when there is no such event.
func (g *FakeGraph) EditEvent(userID, eventID string, fn func(e *Event)) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.mailboxOf(userID).event(eventID)
	if e == nil {
		return false
	}
	fn(e)
	e.ChangeKey = g.changeKey()
	e.seq = g.serial
	return true
}

// RemoveEvent deletes the user's event id, as the person would in Outlook;
// false when there is no such event.
func (g *FakeGraph) RemoveEvent(userID, eventID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	mb := g.mailboxOf(userID)
	e := mb.event(eventID)
	if e == nil {
		return false
	}
	g.removeEvent(mb, e)
	return true
}

// ExpireDelta makes every delta token of the user's answer status — 410
// Gone, or 400 syncStateNotFound for any other — until a fresh query; 0
// lifts it.
func (g *FakeGraph) ExpireDelta(userID string, status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mailboxOf(userID).deltaExpired = status
}

// removeEvent deletes e from mb, logging its deletion.
func (g *FakeGraph) removeEvent(mb *mailbox, e *Event) {
	mb.events = slices.DeleteFunc(mb.events, func(x *Event) bool { return x == e })
	g.serial++
	mb.removed = append(mb.removed, tombstone{id: e.ID, calendarID: e.CalendarID, seq: g.serial})
}

// delta answers GET /users/{userID}/calendarView/delta.
func (g *FakeGraph) delta(userID string, query url.Values, prefer string) answer {
	u := g.userByID(userID, false)
	if u == nil {
		return notFound(userID)
	}
	if a, refused := mailboxRefused(u); refused {
		return a
	}
	mb := g.mailboxOf(u.ID)
	key := strings.ToLower(u.ID)
	var st deltaState
	skip, deltaTok := query.Get("$skiptoken"), query.Get("$deltatoken")
	switch {
	case skip == "" && deltaTok == "":
		from, err1 := time.Parse(time.RFC3339, query.Get("startDateTime"))
		to, err2 := time.Parse(time.RFC3339, query.Get("endDateTime"))
		if err1 != nil || err2 != nil || !to.After(from) {
			return graphError(http.StatusBadRequest, "ErrorInvalidParameter", "startDateTime and endDateTime are required.")
		}
		mb.deltaExpired = 0
		st = deltaState{user: key, from: from, to: to, upto: g.serial}
	default:
		if mb.deltaExpired != 0 {
			if mb.deltaExpired == http.StatusGone {
				return graphError(http.StatusGone, "Gone", "The sync state is no longer valid.")
			}
			return graphError(mb.deltaExpired, "syncStateNotFound", "The sync state generation is not found.")
		}
		token := skip + deltaTok
		known, ok := g.deltas[token]
		if !ok || known.user != key || (skip != "" && deltaTok != "") {
			return graphError(http.StatusBadRequest, "syncStateNotFound", "The sync state generation is not found.")
		}
		st = *known
		if deltaTok != "" {
			st = deltaState{user: key, from: known.from, to: known.to, since: known.upto, upto: g.serial}
		}
	}

	size := 10
	if m := deltaPageSize.FindStringSubmatch(prefer); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			size = n
		}
	}
	if g.DeltaPageSize > 0 {
		size = min(size, g.DeltaPageSize)
	}
	utc := strings.Contains(prefer, `outlook.timezone="UTC"`)

	changes := g.deltaChanges(mb, st, utc)
	end := min(st.offset+size, len(changes))
	value := make([]any, 0, end-st.offset)
	for _, c := range changes[st.offset:end] {
		value = append(value, c)
	}
	body := map[string]any{"value": value}
	link := "https://" + GraphHost + "/v1.0/users/" + u.ID + "/calendarView/delta?"
	if g.DeltaLinksOddCase {
		link = "https://" + GraphHost + "/v1.0/users/" + strings.ToUpper(u.ID) + "/calendarview/delta?"
	}
	if end < len(changes) {
		next := st
		next.offset = end
		body["@odata.nextLink"] = link + "$skiptoken=" + g.deltaToken(next)
	} else {
		body["@odata.deltaLink"] = link + "$deltatoken=" + g.deltaToken(deltaState{user: key, from: st.from, to: st.to, upto: st.upto})
	}
	return answer{status: http.StatusOK, body: body}
}

// deltaToken keeps st under a fresh token and answers it.
func (g *FakeGraph) deltaToken(st deltaState) string {
	if g.deltas == nil {
		g.deltas = map[string]*deltaState{}
	}
	g.serial++
	token := "T" + strconv.Itoa(g.serial)
	g.deltas[token] = &st
	return token
}

// deltaChanges are the default calendar's changes st stands for, in the
// order they were made: since 0 every event in the window; else every event
// changed after since up to upto — whole in the window, removed outside it
// — and every deletion.
func (g *FakeGraph) deltaChanges(mb *mailbox, st deltaState, utc bool) []map[string]any {
	cal := mb.calendar("")
	type entry struct {
		seq int
		out map[string]any
	}
	var all []entry
	for _, e := range mb.events {
		if e.CalendarID != cal.ID || e.seq > st.upto || e.seq <= st.since {
			continue
		}
		in := overlaps(e, st.from, st.to)
		switch {
		case in:
			all = append(all, entry{e.seq, deltaEventJSON(e, utc)})
		case st.since > 0:
			all = append(all, entry{e.seq, removedJSON(e.ID)})
		}
	}
	if st.since > 0 {
		for _, r := range mb.removed {
			if r.calendarID == cal.ID && r.seq > st.since && r.seq <= st.upto {
				all = append(all, entry{r.seq, removedJSON(r.id)})
			}
		}
	}
	slices.SortStableFunc(all, func(a, b entry) int { return a.seq - b.seq })
	out := make([]map[string]any, len(all))
	for i, e := range all {
		out[i] = e.out
	}
	return out
}

func removedJSON(id string) map[string]any {
	return map[string]any{"id": id, "@removed": map[string]any{"reason": "deleted"}}
}

// deltaEventJSON is e as the delta answers it: whole, its times in UTC when
// utc, with seven fractional digits.
func deltaEventJSON(e *Event, utc bool) map[string]any {
	out := eventJSON(e, nil)
	out["start"], out["end"] = deltaDateTime(e.Start, utc), deltaDateTime(e.End, utc)
	return out
}

func deltaDateTime(d DateTimeZone, utc bool) DateTimeZone {
	t, ok := instantOf(d)
	if !ok {
		return d
	}
	if !utc {
		return DateTimeZone{DateTime: strings.SplitN(d.DateTime, ".", 2)[0] + ".0000000", TimeZone: d.TimeZone}
	}
	return DateTimeZone{DateTime: t.UTC().Format("2006-01-02T15:04:05") + ".0000000", TimeZone: "UTC"}
}

// windowsZones are the Windows zone names the fake reads besides IANA's.
var windowsZones = map[string]string{
	"W. Europe Standard Time": "Europe/Berlin",
	"GMT Standard Time":       "Europe/London",
	"Pacific Standard Time":   "America/Los_Angeles",
}

// instantOf is d's instant: its dateTime in its zone, IANA or one of
// windowsZones.
func instantOf(d DateTimeZone) (time.Time, bool) {
	loc := time.UTC
	if z := d.TimeZone; z != "" && z != "UTC" {
		if name, ok := windowsZones[z]; ok {
			z = name
		}
		l, err := time.LoadLocation(z)
		if err != nil {
			return time.Time{}, false
		}
		loc = l
	}
	t, err := time.ParseInLocation("2006-01-02T15:04:05", d.DateTime, loc)
	return t, err == nil
}

// overlaps reports whether e's times overlap from..to.
func overlaps(e *Event, from, to time.Time) bool {
	start, ok1 := instantOf(e.Start)
	end, ok2 := instantOf(e.End)
	if !ok1 || !ok2 {
		return false
	}
	if end.Equal(start) {
		return !start.Before(from) && start.Before(to)
	}
	return start.Before(to) && end.After(from)
}
