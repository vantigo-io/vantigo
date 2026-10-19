package calendar

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/teambition/rrule-go"

	"github.com/vantigo-io/vantigo/server/internal/calendar/store"
)

// This file is recurrence (calendar design D4): an RFC 5545 RRULE subset,
// parsed with teambition/rrule-go, stored re-serialised in one canonical form,
// and expanded in the appointment's own zone.
//
// The subset: FREQ of DAILY, WEEKLY or MONTHLY; INTERVAL 1 to 99; BYDAY with
// plain weekdays for WEEKLY and one ordinal (2TU, -1FR) for MONTHLY;
// BYMONTHDAY with one day for MONTHLY; one of COUNT (at most 730) or UNTIL (a
// date at most five years after the start), or neither. Anything else is a
// field error on recurrence.rule. Every rule in it produces at most one
// occurrence a day, which is what lets an occurrence be named by its date.
//
// Expansion is floating: the appointment's start is read as wall-clock in its
// zone and handed to rrule-go dressed as UTC, so the rule steps over civil
// days and never meets a daylight-saving shift; each occurrence's wall-clock
// is then put back into the zone (time.Date in it) and converted to an
// instant. A 09:00 Oslo series is 09:00 in Oslo in March and in October, its
// UTC hour shifting with the clock. rrule-go, unlike RFC 5545, counts the
// start as an occurrence only when the rule produces it, so a rule that does
// not produce the start's own day is refused (seriesFrom): the start is
// always the first occurrence, as RFC 5545 has it, and COUNT counts it.

// The rule's limits.
const (
	maxRuleInterval = 99
	maxRuleCount    = 730
	maxRuleYears    = 5
	// maxRuleLength is the stored column's width; a longer rule is refused
	// before it is parsed at all.
	maxRuleLength = 500
	// maxEcho is the most of a request's own text a refusal repeats back.
	maxEcho = 60
)

// clip is v cut to maxEcho characters, for a refusal that names what it
// refused without repeating whatever a request sent.
func clip(v string) string {
	if utf8.RuneCountInString(v) <= maxEcho {
		return v
	}
	return string([]rune(v)[:maxEcho-1]) + "…"
}

// ruleParts is every part the subset reads; any other is refused by name.
var ruleParts = map[string]bool{
	"FREQ": true, "INTERVAL": true, "BYDAY": true, "BYMONTHDAY": true, "COUNT": true, "UNTIL": true,
}

// weekdayCodes are RFC 5545's weekday names in rrule-go's order, Monday 0.
var weekdayCodes = [...]string{"MO", "TU", "WE", "TH", "FR", "SA", "SU"}

// rule is a rule of the subset, parsed.
type rule struct {
	freq       rrule.Frequency
	interval   int
	byDay      []rrule.Weekday // plain for WEEKLY, one ordinal for MONTHLY
	byMonthDay int             // MONTHLY, 0 when not given
	count      int             // 0 when not given
	until      time.Time       // a civil date, zero when not given
}

// ruleSubset is what every refusal of the grammar ends with, so
// a person reading one knows what is accepted.
const ruleSubset = "a rule repeats DAILY, WEEKLY or MONTHLY with INTERVAL, BYDAY, BYMONTHDAY and COUNT or UNTIL"

// parseRule reads raw as a rule of the subset: the rule, or the message that
// refuses it. loc is the appointment's zone, which an UNTIL given as a UTC
// date-time is read in to find its day.
func parseRule(raw string, loc *time.Location) (rule, string) {
	if len(raw) > maxRuleLength {
		return rule{}, fmt.Sprintf("A rule is at most %d characters", maxRuleLength)
	}
	v := strings.ToUpper(strings.TrimSpace(raw))
	v = strings.TrimPrefix(v, "RRULE:")
	if v == "" {
		return rule{}, "A rule is required"
	}
	if strings.ContainsAny(v, "\r\n") || strings.Contains(v, "DTSTART") {
		return rule{}, "A rule carries no DTSTART: the appointment's start is the series' start"
	}
	seen := map[string]bool{}
	untilRaw := ""
	for _, part := range strings.Split(v, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || value == "" {
			return rule{}, fmt.Sprintf("'%s' is not a rule part: %s", clip(part), ruleSubset)
		}
		if !ruleParts[key] {
			return rule{}, fmt.Sprintf("%s is not supported: %s", clip(key), ruleSubset)
		}
		if seen[key] {
			return rule{}, fmt.Sprintf("%s is given more than once", key)
		}
		seen[key] = true
		if key == "UNTIL" {
			untilRaw = value
		}
	}
	// rrule-go's own words are not the client's: a value it cannot read is
	// answered in this module's.
	opt, err := rrule.StrToROptionInLocation(v, time.UTC)
	if err != nil {
		return rule{}, "This is not a rule that can be read: " + ruleSubset
	}
	r := rule{freq: opt.Freq, interval: opt.Interval, count: opt.Count}
	switch opt.Freq {
	case rrule.DAILY, rrule.WEEKLY, rrule.MONTHLY:
	default:
		return rule{}, "FREQ is DAILY, WEEKLY or MONTHLY"
	}
	if !seen["INTERVAL"] {
		r.interval = 1
	} else if r.interval < 1 || r.interval > maxRuleInterval {
		return rule{}, fmt.Sprintf("INTERVAL is 1 to %d", maxRuleInterval)
	}
	if msg := r.readByDay(opt.Byweekday); msg != "" {
		return rule{}, msg
	}
	if len(opt.Bymonthday) > 0 {
		switch d := opt.Bymonthday[0]; {
		case r.freq != rrule.MONTHLY:
			return rule{}, "BYMONTHDAY is for a MONTHLY rule"
		case len(opt.Bymonthday) != 1:
			return rule{}, "A monthly rule's BYMONTHDAY is one day"
		case len(r.byDay) > 0:
			return rule{}, "A monthly rule repeats by BYDAY or by BYMONTHDAY, not both"
		case d == 0 || d > 31 || d < -31:
			return rule{}, "BYMONTHDAY is 1 to 31, or -1 to -31 counting from the month's end"
		default:
			r.byMonthDay = d
		}
	}
	if seen["COUNT"] && seen["UNTIL"] {
		return rule{}, "A rule ends by COUNT or by UNTIL, not both"
	}
	if seen["COUNT"] && (r.count < 1 || r.count > maxRuleCount) {
		return rule{}, fmt.Sprintf("COUNT is 1 to %d", maxRuleCount)
	}
	if seen["UNTIL"] {
		if strings.HasSuffix(untilRaw, "Z") {
			// UNTIL is inclusive, so an instant exactly at midnight in the
			// zone ends the day before it, not the one it begins.
			r.until = civil(opt.Until.Add(-time.Second).In(loc))
		} else {
			r.until = civil(opt.Until)
		}
	}
	return r, ""
}

// readByDay takes BYDAY for the rule's frequency: plain weekdays, each once,
// for WEEKLY; one ordinal weekday for MONTHLY; none for DAILY.
func (r *rule) readByDay(days []rrule.Weekday) string {
	if len(days) == 0 {
		return ""
	}
	switch r.freq {
	case rrule.WEEKLY:
		seen := map[int]bool{}
		for _, d := range days {
			if d.N() != 0 {
				return "A weekly rule's BYDAY names plain weekdays, such as MO,WE"
			}
			if seen[d.Day()] {
				return fmt.Sprintf("BYDAY names %s more than once", weekdayCodes[d.Day()])
			}
			seen[d.Day()] = true
		}
		r.byDay = slices.SortedFunc(slices.Values(days), func(a, b rrule.Weekday) int { return a.Day() - b.Day() })
	case rrule.MONTHLY:
		if len(days) != 1 || days[0].N() == 0 || days[0].N() > 5 || days[0].N() < -5 {
			return "A monthly rule's BYDAY is one ordinal weekday, such as 2TU or -1FR"
		}
		r.byDay = days
	default:
		return "BYDAY is for a WEEKLY or a MONTHLY rule"
	}
	return ""
}

// String is the rule's canonical form, the one stored:
// FREQ;INTERVAL;BYDAY;BYMONTHDAY;COUNT or UNTIL, in that order, INTERVAL
// always written, weekdays Monday first, an ordinal without its sign when
// positive, UNTIL a date.
func (r rule) String() string {
	parts := []string{"FREQ=" + r.freq.String(), "INTERVAL=" + strconv.Itoa(r.interval)}
	if len(r.byDay) > 0 {
		days := make([]string, len(r.byDay))
		for i, d := range r.byDay {
			days[i] = weekdayCodes[d.Day()]
			if d.N() != 0 {
				days[i] = strconv.Itoa(d.N()) + days[i]
			}
		}
		parts = append(parts, "BYDAY="+strings.Join(days, ","))
	}
	if r.byMonthDay != 0 {
		parts = append(parts, "BYMONTHDAY="+strconv.Itoa(r.byMonthDay))
	}
	if r.count > 0 {
		parts = append(parts, "COUNT="+strconv.Itoa(r.count))
	}
	if !r.until.IsZero() {
		parts = append(parts, "UNTIL="+r.until.Format("20060102"))
	}
	return strings.Join(parts, ";")
}

// bounded reports whether the rule ends.
func (r rule) bounded() bool { return r.count > 0 || !r.until.IsZero() }

// untilMessage is why the rule's UNTIL does not fit a series starting on
// startDay, "" when it does or there is none.
func (r rule) untilMessage(startDay time.Time) string {
	switch {
	case r.until.IsZero():
		return ""
	case r.until.Before(startDay):
		return "UNTIL is before the appointment starts"
	case r.until.After(startDay.AddDate(maxRuleYears, 0, 0)):
		return fmt.Sprintf("UNTIL is at most %d years after the appointment starts", maxRuleYears)
	}
	return ""
}

// series is one appointment's rule ready to expand.
type series struct {
	rr       *rrule.RRule
	allDay   bool
	loc      *time.Location
	start    time.Time     // floating: the start's wall-clock in loc, dressed as UTC
	duration time.Duration // timed: each occurrence's length
	spanDays int           // all-day: each occurrence's last day minus its first
	last     time.Time     // the last occurrence's date when known, zero otherwise
}

// span is one occurrence's time: two civil dates, or two instants.
type span struct {
	allDay             bool
	startDate, endDate time.Time
	startsAt, endsAt   time.Time
}

// floating is t's wall-clock in loc, dressed as UTC.
func floating(t time.Time, loc *time.Location) time.Time {
	w := t.In(loc)
	return time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, time.UTC)
}

// newSeries is r expanding from the appointment's time s, in loc.
func newSeries(r rule, s span, loc *time.Location) (*series, error) {
	sr := &series{allDay: s.allDay, loc: loc}
	if s.allDay {
		sr.start = civil(s.startDate)
		sr.spanDays = daysBetween(civil(s.startDate), civil(s.endDate))
	} else {
		sr.start = floating(s.startsAt, loc)
		sr.duration = s.endsAt.Sub(s.startsAt)
	}
	opt := rrule.ROption{Freq: r.freq, Interval: r.interval, Count: r.count, Byweekday: r.byDay, Dtstart: sr.start}
	if r.byMonthDay != 0 {
		opt.Bymonthday = []int{r.byMonthDay}
	}
	if !r.until.IsZero() {
		opt.Until = r.until.Add(24*time.Hour - time.Second)
	}
	rr, err := rrule.NewRRule(opt)
	if err != nil {
		return nil, fmt.Errorf("calendar: build the rule %q: %w", r.String(), err)
	}
	sr.rr = rr
	return sr, nil
}

// seriesOf is a stored recurring appointment's series, nil when it is single.
// A stored rule is canonical, so a refusal here is a fault, not a field error.
func seriesOf(a store.CalendarAppointment) (*series, error) {
	if a.Rrule == nil {
		return nil, nil
	}
	loc, err := time.LoadLocation(a.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("calendar: load appointment %d's zone %q: %w", a.ID, a.TimeZone, err)
	}
	r, msg := parseRule(*a.Rrule, loc)
	if msg != "" {
		return nil, fmt.Errorf("calendar: appointment %d's stored rule %q: %s", a.ID, *a.Rrule, msg)
	}
	sr, err := newSeries(r, spanOf(a), loc)
	if err != nil {
		return nil, err
	}
	if last, ok := fromPgDate(a.SeriesLastDate); ok {
		sr.last = last
	}
	return sr, nil
}

// spanOf is a stored appointment's own time.
func spanOf(a store.CalendarAppointment) span {
	s := span{allDay: a.AllDay}
	if a.AllDay {
		s.startDate, _ = fromPgDate(a.StartDate)
		s.endDate, _ = fromPgDate(a.EndDate)
		return s
	}
	if a.StartsAt != nil && a.EndsAt != nil {
		s.startsAt, s.endsAt = a.StartsAt.UTC(), a.EndsAt.UTC()
	}
	return s
}

// startDay is the day the series starts on, in its zone.
func (sr *series) startDay() time.Time { return civil(sr.start) }

// on is every occurrence whose date is from..to, both counted, as floating
// starts in order. A span before the series starts or after its last day
// answers nothing without asking the rule, which would otherwise step from
// the start to the far end of whatever it was asked.
func (sr *series) on(from, to time.Time) []time.Time {
	from, to = civil(from), civil(to)
	if start := sr.startDay(); from.Before(start) {
		from = start
	}
	if !sr.last.IsZero() && to.After(sr.last) {
		to = sr.last
	}
	if to.Before(from) {
		return nil
	}
	return sr.rr.Between(from, to.Add(24*time.Hour-time.Second), true)
}

// produces reports whether the rule puts an occurrence on date — the test
// an occurrence's endpoints apply, by expanding the rule to that day.
func (sr *series) produces(date time.Time) bool { return len(sr.on(date, date)) > 0 }

// lastDate is the last occurrence's date, for a rule that ends; ok is false
// for one that never does, and for one that produces nothing at all.
func (sr *series) lastDate() (time.Time, bool) {
	all := sr.rr.All()
	if len(all) == 0 {
		return time.Time{}, false
	}
	return civil(all[len(all)-1]), true
}

// occurrence is the occurrence the series starts at floating f: the same
// wall-clock in the zone, the appointment's own length.
func (sr *series) occurrence(f time.Time) span {
	if sr.allDay {
		d := civil(f)
		return span{allDay: true, startDate: d, endDate: addDays(d, sr.spanDays)}
	}
	starts := time.Date(f.Year(), f.Month(), f.Day(), f.Hour(), f.Minute(), f.Second(), 0, sr.loc).UTC()
	return span{startsAt: starts, endsAt: starts.Add(sr.duration)}
}

// reachDays is how many days before an occurrence's date it may still cover
// a later day: its own length, plus the two days two zones' midnights can be
// apart.
func (sr *series) reachDays() int {
	if sr.allDay {
		return sr.spanDays + 2
	}
	return int(sr.duration.Hours()/24) + 2
}
