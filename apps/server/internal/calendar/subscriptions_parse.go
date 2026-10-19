package calendar

import (
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	ics "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"

	"github.com/vantigo-io/vantigo/server/internal/calendar/gen"
)

// This file is a subscribed calendar parsed on read (calendar feed design
// D8): the body the worker last fetched, read with golang-ical, and every
// VEVENT that overlaps the window answered as an item of kind
// calendar.subscription. Nothing of it is ever written to the module's
// tables, and nothing of it reaches the person's own feed — the feed is built
// from the appointments and the providers, and this is neither.
//
// The time rules: a DTSTART of VALUE=DATE is an all-day item, its DTEND
// exclusive; one ending in Z is an instant; one with a TZID is wall-clock in
// the zone that TZID resolves to (zoneResolver); one with neither is floating
// and read in the viewer's zone. A series is its RRULE expanded with rrule-go
// the module's own way (recurrence.go): DTSTART dressed as floating in its
// zone, each occurrence put back into it, so a 09:00 Oslo series is 09:00 in
// Oslo on both sides of the clock change. EXDATE removes an occurrence; a
// VEVENT with a RECURRENCE-ID replaces the occurrence at that instant,
// wherever it moved to. RDATE, TRANSP and CLASS are not read.

// subscriptionKind is the kind of a subscribed calendar's item.
const subscriptionKind = "calendar.subscription"

// The bounds of one read.
const (
	// maxSubscriptionTitle is the most of a SUMMARY an item carries — the
	// same width as an appointment's title.
	maxSubscriptionTitle = maxTitle
	// maxRuleOccurrences is the most occurrences one rule contributes, and
	// maxSubscriptionItems the most items one subscription does; the rest
	// are dropped with a log line.
	maxRuleOccurrences   = 1000
	maxSubscriptionItems = 5000
	// maxRuleSteps bounds how far one rule is iterated to reach the window
	// at all: a rule stepping by the minute from years ago would otherwise
	// be walked to the window one step at a time.
	maxRuleSteps = 100_000
)

// errNotACalendar is a body that does not begin, after blank lines, with
// BEGIN:VCALENDAR.
var errNotACalendar = errors.New("calendar: the body is not an iCalendar object")

// parseCalendarBody is body as a calendar: it must begin, after leading
// blank lines and a byte-order mark, with BEGIN:VCALENDAR, and parse.
func parseCalendarBody(body string) (*ics.Calendar, error) {
	trimmed := strings.TrimLeft(body, " \t\r\n\ufeff")
	const begin = "BEGIN:VCALENDAR"
	if len(trimmed) < len(begin) || !strings.EqualFold(trimmed[:len(begin)], begin) {
		return nil, errNotACalendar
	}
	return ics.ParseCalendar(strings.NewReader(trimmed))
}

// subscriptionSource is what an item carries of the subscription it came
// from.
type subscriptionSource struct {
	id       int64
	name     string
	colour   string
	busyOnly bool
}

// parseSubscriptionItems is every event of body that overlaps w, as items of
// sub, titled in lang. A body that does not parse contributes nothing: the
// worker refuses to store one, so it is a body stored before a rule
// changed, and the next fetch replaces it. log may be nil.
func parseSubscriptionItems(body string, sub subscriptionSource, w window, lang string, log *slog.Logger) []gen.CalendarItem {
	cal, err := parseCalendarBody(body)
	if err != nil {
		logDebug(log, "calendar: a subscription's calendar could not be parsed; it contributes nothing",
			"subscription_id", sub.id, "error", err.Error())
		return nil
	}
	zones := newZoneResolver(cal)
	wd := wordsFor(lang)

	masters := map[string][]subEvent{}
	overrides := map[string][]subEvent{}
	var order []string
	for i, ev := range cal.Events() {
		e, ok := readEvent(&ev.ComponentBase, zones, w.loc)
		if !ok {
			continue
		}
		if e.uid == "" {
			e.uid = "event-" + strconv.Itoa(i+1)
		}
		if _, seen := masters[e.uid]; !seen {
			if _, seen := overrides[e.uid]; !seen {
				order = append(order, e.uid)
			}
		}
		if e.recurrenceID != nil {
			overrides[e.uid] = append(overrides[e.uid], e)
		} else {
			masters[e.uid] = append(masters[e.uid], e)
		}
	}

	var out []gen.CalendarItem
	emit := func(e subEvent, stamp string, sp span) {
		if e.cancelled || !overlaps(sp, w) {
			return
		}
		id := strconv.FormatInt(sub.id, 10) + ":" + e.uid
		if stamp != "" {
			id += ":" + stamp
		}
		it := gen.CalendarItem{
			Kind: subscriptionKind, Id: id, Title: subscriptionTitle(e, sub.busyOnly, wd), AllDay: sp.allDay,
			Subtitle: &sub.name,
			Subscription: &gen.CalendarItemSubscription{
				Id: sub.id, Name: sub.name, Colour: gen.CalendarSubscriptionColour(sub.colour), BusyOnly: sub.busyOnly,
			},
		}
		setSpan(&it, sp)
		out = append(out, it)
	}
	for _, uid := range order {
		moved := map[time.Time]bool{}
		for _, o := range overrides[uid] {
			moved[o.recurrenceID.key()] = true
			emit(o, o.recurrenceID.stamp(), o.span(o.start.t))
		}
		for _, m := range masters[uid] {
			if m.cancelled {
				continue
			}
			if m.rule == "" {
				emit(m, "", m.span(m.start.t))
				continue
			}
			starts, capped, ok := m.expand(w)
			if !ok {
				logDebug(log, "calendar: a subscribed event's rule could not be read; it is shown once at its start",
					"subscription_id", sub.id)
				emit(m, "", m.span(m.start.t))
				continue
			}
			if capped {
				logInfo(log, "calendar: a subscribed rule produced more occurrences than are taken; the rest are dropped",
					"subscription_id", sub.id, "taken", maxRuleOccurrences)
			}
			for _, s := range starts {
				occ := icsTime{date: m.start.date, t: s, loc: m.start.loc}
				if moved[occ.key()] || m.excluded(occ) {
					continue
				}
				emit(m, occ.stamp(), m.span(s))
			}
		}
	}
	if len(out) > maxSubscriptionItems {
		distance := func(it gen.CalendarItem) time.Duration {
			at := w.fromAt
			if it.StartsAt != nil {
				at = *it.StartsAt
			} else if it.StartDate != nil {
				at = wallClock(it.StartDate.Time, w.loc)
			}
			if d := at.Sub(w.fromAt); d >= 0 {
				return d
			}
			return w.fromAt.Sub(at)
		}
		sort.SliceStable(out, func(i, j int) bool { return distance(out[i]) < distance(out[j]) })
		logInfo(log, "calendar: a subscription has more items in the window than are taken; the rest are dropped",
			"subscription_id", sub.id, "items", len(out), "taken", maxSubscriptionItems)
		out = out[:maxSubscriptionItems]
	}
	if len(zones.fallbacks) > 0 {
		args := []any{"subscription_id", sub.id}
		for step, n := range zones.fallbacks {
			args = append(args, step, n)
		}
		logDebug(log, "calendar: a subscription's TZIDs were resolved by a fallback", args...)
	}
	return out
}

// subscriptionTitle is an event's title on the wire: empty for a busy-only
// subscription, which the app labels Busy; SUMMARY, already unescaped by the
// parser, on one line and cut to the title's width; or "(untitled)" in the
// person's language.
func subscriptionTitle(e subEvent, busyOnly bool, w words) string {
	if busyOnly {
		return ""
	}
	t := strings.TrimSpace(oneLine(e.summary))
	if t == "" {
		return w.untitled
	}
	if utf8.RuneCountInString(t) > maxSubscriptionTitle {
		t = strings.TrimSpace(string([]rune(t)[:maxSubscriptionTitle]))
	}
	return t
}

// icsTime is one DTSTART, DTEND, RECURRENCE-ID or EXDATE value, read: a
// civil date when date, an instant otherwise, with the zone it was written
// in — UTC for a value in Z, the viewer's for a floating one.
type icsTime struct {
	date bool
	t    time.Time
	loc  *time.Location
}

// key is what an occurrence is matched by: its date, or its instant in UTC.
func (v icsTime) key() time.Time {
	if v.date {
		return civil(v.t)
	}
	return v.t.UTC()
}

// stamp is the occurrence's part of an item's id: 20261103 for a date,
// 20261103T080000Z for an instant.
func (v icsTime) stamp() string {
	if v.date {
		return v.t.Format("20060102")
	}
	return v.t.UTC().Format("20060102T150405Z")
}

// subEvent is one VEVENT, read.
type subEvent struct {
	uid          string
	summary      string
	cancelled    bool
	start        icsTime
	length       time.Duration // timed: the end minus the start, 0 for a point
	days         int           // all-day: the last day minus the first
	rule         string
	exdates      []icsTime
	recurrenceID *icsTime
}

// readEvent reads one VEVENT; ok is false for one without a DTSTART that
// can be read, which shows nowhere.
func readEvent(cb *ics.ComponentBase, zones *zoneResolver, floating *time.Location) (subEvent, bool) {
	var e subEvent
	if p := propOf(cb, "UID"); p != nil {
		e.uid = strings.TrimSpace(p.Value)
	}
	if p := propOf(cb, "SUMMARY"); p != nil {
		e.summary = p.Value
	}
	if p := propOf(cb, "STATUS"); p != nil && strings.EqualFold(strings.TrimSpace(p.Value), "CANCELLED") {
		e.cancelled = true
	}
	starts := readTimes(propOf(cb, "DTSTART"), zones, floating)
	if len(starts) != 1 {
		return e, false
	}
	e.start = starts[0]
	var dur time.Duration
	hasDur := false
	if p := propOf(cb, "DURATION"); p != nil {
		dur, hasDur = parseICalDuration(p.Value)
	}
	ends := readTimes(propOf(cb, "DTEND"), zones, floating)
	if e.start.date {
		switch {
		case len(ends) == 1:
			last := civil(ends[0].t)
			if ends[0].date {
				last = addDays(last, -1) // DTEND is exclusive
			}
			e.days = daysBetween(civil(e.start.t), last)
		case hasDur:
			e.days = int(dur/(24*time.Hour)) - 1
		}
		e.days = max(e.days, 0)
	} else {
		switch {
		case len(ends) == 1 && !ends[0].date:
			e.length = ends[0].t.Sub(e.start.t)
		case hasDur:
			e.length = dur
		}
		e.length = max(e.length, 0) // an end before its start is a point
	}
	if p := propOf(cb, "RRULE"); p != nil {
		e.rule = strings.TrimSpace(p.Value)
	}
	for _, p := range propsOf(cb, "EXDATE") {
		e.exdates = append(e.exdates, readTimes(p, zones, floating)...)
	}
	if ids := readTimes(propOf(cb, "RECURRENCE-ID"), zones, floating); len(ids) == 1 {
		e.recurrenceID = &ids[0]
	}
	return e, true
}

// span is the event's time when it starts at start — a civil date for an
// all-day event, an instant for a timed one.
func (e subEvent) span(start time.Time) span {
	if e.start.date {
		d := civil(start)
		return span{allDay: true, startDate: d, endDate: addDays(d, e.days)}
	}
	return span{startsAt: start.UTC(), endsAt: start.UTC().Add(e.length)}
}

// excluded reports whether an EXDATE removes the occurrence occ: one naming
// its instant, or for an EXDATE written as a date, its day in the event's
// zone.
func (e subEvent) excluded(occ icsTime) bool {
	for _, x := range e.exdates {
		switch {
		case x.date == occ.date && x.key().Equal(occ.key()):
			return true
		case x.date && !occ.date && civil(x.t).Equal(civil(occ.t.In(occ.loc))):
			return true
		}
	}
	return false
}

// expand is the starts of the event's rule that may overlap w — instants for
// a timed event, civil dates for an all-day one — in order, at most
// maxRuleOccurrences (capped reports a drop); ok is false for a rule
// rrule-go refuses.
//
// The rule is stepped over floating wall-clock in the start's zone, as A's
// series are, and widened back from the window by the event's own length,
// since an occurrence that began before the window may still cover it.
func (e subEvent) expand(w window) (starts []time.Time, capped, ok bool) {
	raw := strings.TrimPrefix(strings.ToUpper(e.rule), "RRULE:")
	opt, err := rrule.StrToROptionInLocation(raw, time.UTC)
	if err != nil {
		return nil, false, false
	}
	loc := e.start.loc
	if e.start.date {
		loc = time.UTC
	}
	opt.Dtstart = floating(e.start.t, loc)
	if !e.start.date && untilInUTC(raw) {
		opt.Until = floating(opt.Until, loc)
	}
	rr, err := rrule.NewRRule(*opt)
	if err != nil {
		return nil, false, false
	}
	reach := e.length + 48*time.Hour
	if e.start.date {
		reach = time.Duration(e.days+2) * 24 * time.Hour
	}
	from := floating(w.fromAt, loc).Add(-reach)
	to := floating(w.toAt, loc).Add(24 * time.Hour)
	next := rr.Iterator()
	for step := 0; step < maxRuleSteps; step++ {
		f, more := next()
		if !more || f.After(to) {
			break
		}
		if f.Before(from) {
			continue
		}
		if len(starts) == maxRuleOccurrences {
			return starts, true, true
		}
		if e.start.date {
			starts = append(starts, civil(f))
		} else {
			starts = append(starts, time.Date(f.Year(), f.Month(), f.Day(), f.Hour(), f.Minute(), f.Second(), 0, loc).UTC())
		}
	}
	return starts, false, true
}

// untilInUTC reports whether the rule's UNTIL is an instant in UTC, which
// must be dressed as floating in the start's zone to be compared with the
// floating occurrences.
func untilInUTC(rule string) bool {
	for _, part := range strings.Split(rule, ";") {
		if k, v, ok := strings.Cut(part, "="); ok && k == "UNTIL" {
			return strings.HasSuffix(v, "Z")
		}
	}
	return false
}

// readTimes is every value of a date or date-time property — EXDATE may
// carry several, comma separated — skipping one that cannot be read. p may
// be nil.
func readTimes(p *ics.IANAProperty, zones *zoneResolver, floatingLoc *time.Location) []icsTime {
	if p == nil {
		return nil
	}
	dateValued := strings.EqualFold(param(p, "VALUE"), "DATE")
	tzid := param(p, "TZID")
	var out []icsTime
	for _, v := range strings.Split(p.Value, ",") {
		v = strings.TrimSpace(v)
		switch {
		case dateValued || len(v) == len("20060102"):
			if d, err := time.Parse("20060102", v); err == nil {
				out = append(out, icsTime{date: true, t: d, loc: time.UTC})
			}
		case strings.HasSuffix(v, "Z"):
			if t, err := time.Parse("20060102T150405Z", v); err == nil {
				out = append(out, icsTime{t: t, loc: time.UTC})
			}
		default:
			loc := floatingLoc
			if tzid != "" {
				loc = zones.resolve(tzid)
			}
			if t, err := time.ParseInLocation("20060102T150405", v, loc); err == nil {
				out = append(out, icsTime{t: t, loc: loc})
			}
		}
	}
	return out
}

// zoneResolver resolves a calendar's TZIDs, in order: Go's tzdata by that
// name; the calendar's VTIMEZONE of that TZID carrying X-LIC-LOCATION
// (libical's and Google's hint) naming a zone Go knows; CLDR's windowsZones
// table; the VTIMEZONE's STANDARD offset as a fixed zone — right in winter,
// an hour off in summer, still better than a day off; and UTC. Each answer
// is kept for the read, and each fallback counted for one debug line.
type zoneResolver struct {
	vtimezones map[string]*ics.VTimezone
	resolved   map[string]*time.Location
	fallbacks  map[string]int
}

func newZoneResolver(cal *ics.Calendar) *zoneResolver {
	z := &zoneResolver{vtimezones: map[string]*ics.VTimezone{}, resolved: map[string]*time.Location{}, fallbacks: map[string]int{}}
	for _, c := range cal.Components {
		if tz, ok := c.(*ics.VTimezone); ok {
			if p := propOf(&tz.ComponentBase, "TZID"); p != nil {
				z.vtimezones[strings.TrimSpace(p.Value)] = tz
			}
		}
	}
	return z
}

// resolve is tzid's zone.
func (z *zoneResolver) resolve(tzid string) *time.Location {
	if loc, ok := z.resolved[tzid]; ok {
		return loc
	}
	loc, step := z.lookup(tzid)
	if step != "" {
		z.fallbacks[step]++
	}
	z.resolved[tzid] = loc
	return loc
}

// lookup is the resolution chain; step names the fallback that answered,
// "" for Go's tzdata.
func (z *zoneResolver) lookup(tzid string) (*time.Location, string) {
	name := strings.Trim(strings.TrimSpace(tzid), `"`)
	if loc, ok := loadZone(name); ok {
		return loc, ""
	}
	vtz := z.vtimezones[tzid]
	if vtz == nil {
		vtz = z.vtimezones[name]
	}
	if vtz != nil {
		if p := propOf(&vtz.ComponentBase, "X-LIC-LOCATION"); p != nil {
			if loc, ok := loadZone(strings.TrimSpace(p.Value)); ok {
				return loc, "x_lic_location"
			}
		}
	}
	if iana, ok := windowsZones[name]; ok {
		if loc, ok := loadZone(iana); ok {
			return loc, "windows_zone"
		}
	}
	if vtz != nil {
		for _, c := range vtz.Components {
			if st, ok := c.(*ics.Standard); ok {
				if p := propOf(&st.ComponentBase, "TZOFFSETTO"); p != nil {
					if secs, ok := parseUTCOffset(p.Value); ok {
						return time.FixedZone(name, secs), "standard_offset"
					}
				}
			}
		}
	}
	return time.UTC, "utc"
}

// loadZone is Go's zone of that name. The empty name and "Local", which
// time.LoadLocation answers with UTC and the server's own zone, are no
// zone a calendar named.
func loadZone(name string) (*time.Location, bool) {
	if name == "" || name == "Local" {
		return nil, false
	}
	loc, err := time.LoadLocation(name)
	return loc, err == nil
}

// parseUTCOffset reads a UTC-OFFSET value, +0100 or -053000, as seconds east
// of UTC.
func parseUTCOffset(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if len(v) != 5 && len(v) != 7 {
		return 0, false
	}
	sign := 1
	switch v[0] {
	case '+':
	case '-':
		sign = -1
	default:
		return 0, false
	}
	n, err := strconv.Atoi(v[1:])
	if err != nil {
		return 0, false
	}
	if len(v) == 5 {
		n *= 100
	}
	h, m, s := n/10000, n/100%100, n%100
	if h > 23 || m > 59 || s > 59 {
		return 0, false
	}
	return sign * (h*3600 + m*60 + s), true
}

// parseICalDuration reads an RFC 5545 duration — P1D, PT1H30M, P2W, -PT15M —
// which REFRESH-INTERVAL, X-PUBLISHED-TTL and DURATION carry.
func parseICalDuration(v string) (time.Duration, bool) {
	s := strings.ToUpper(strings.TrimSpace(v))
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	if !strings.HasPrefix(s, "P") || len(s) < 3 {
		return 0, false
	}
	var d time.Duration
	inTime, num, seen := false, "", false
	for _, r := range s[1:] {
		switch {
		case r >= '0' && r <= '9':
			num += string(r)
		case r == 'T':
			if inTime || num != "" {
				return 0, false
			}
			inTime = true
		default:
			n, err := strconv.ParseInt(num, 10, 64)
			if err != nil || n > 1_000_000 {
				return 0, false
			}
			var unit time.Duration
			switch {
			case r == 'W' && !inTime:
				unit = 7 * 24 * time.Hour
			case r == 'D' && !inTime:
				unit = 24 * time.Hour
			case r == 'H' && inTime:
				unit = time.Hour
			case r == 'M' && inTime:
				unit = time.Minute
			case r == 'S' && inTime:
				unit = time.Second
			default:
				return 0, false
			}
			d += time.Duration(n) * unit
			num, seen = "", true
		}
	}
	if num != "" || !seen {
		return 0, false
	}
	if negative {
		d = -d
	}
	return d, true
}

// propOf is the component's first property of that name, nil when it has
// none.
func propOf(cb *ics.ComponentBase, name string) *ics.IANAProperty {
	for i := range cb.Properties {
		if strings.EqualFold(cb.Properties[i].IANAToken, name) {
			return &cb.Properties[i]
		}
	}
	return nil
}

// propsOf is every property of that name on the component.
func propsOf(cb *ics.ComponentBase, name string) []*ics.IANAProperty {
	var out []*ics.IANAProperty
	for i := range cb.Properties {
		if strings.EqualFold(cb.Properties[i].IANAToken, name) {
			out = append(out, &cb.Properties[i])
		}
	}
	return out
}

// param is a property's first value of that parameter, "" when absent.
func param(p *ics.IANAProperty, name string) string {
	for k, v := range p.ICalParameters {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	}
	return ""
}

// logDebug and logInfo write to log when there is one.
func logDebug(log *slog.Logger, msg string, args ...any) {
	if log != nil {
		log.Debug(msg, args...)
	}
}

func logInfo(log *slog.Logger, msg string, args ...any) {
	if log != nil {
		log.Info(msg, args...)
	}
}
