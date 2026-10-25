package staff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
)

// This file judges what the person sends of their wishes (staff competence
// design S8–S10, §4 "Who writes wishes"; plan readings 18, 19 and 22): each
// whole list by its rules, every failure a field error named by the row's
// place in the list (interests[3].competenceId), never partially applied.
// What depends on the catalogue — a competence or a location that may be
// hidden or archived — is judged under the person's lock on the new and
// changed rows alone (one_to_one.go).

const (
	interestsMax    = 20
	preferencesMax  = 7
	declarationsMax = 50

	preferenceWeightMin = 1
	preferenceWeightMax = 3
	locationIDsMax      = 10
	consecutiveDaysMax  = 14
	hoursPerWeekMax     = 60
	minutesPerDay       = 1440
)

// The preference kinds (S9), in the design's order.
const (
	preferenceWeekdays           = "weekdays"
	preferenceTimeOfDay          = "time_of_day"
	preferenceLocation           = "location"
	preferenceTravel             = "travel"
	preferenceRemote             = "remote"
	preferenceMaxConsecutiveDays = "max_consecutive_days"
	preferenceMaxHoursPerWeek    = "max_hours_per_week"
)

var (
	preferenceKinds = []string{
		preferenceWeekdays, preferenceTimeOfDay, preferenceLocation, preferenceTravel, preferenceRemote,
		preferenceMaxConsecutiveDays, preferenceMaxHoursPerWeek,
	}
	timeOfDayParts   = []string{"morning", "day", "evening", "night"}
	travelExtents    = []string{"none", "regional", "national", "international"}
	remoteShares     = []string{"never", "some", "mostly", "only"}
	availabilityKind = []string{"available", "unavailable", "preferred"}
)

// parseInterests judges an interests replace: at most twenty, each
// competence once — the later of two is refused. Whether each new one may be
// named is the lock's to judge.
func parseInterests(body gen.StaffPersonInterestsRequest) ([]int64, fieldErrors) {
	var f fieldErrors
	if len(body.Interests) > interestsMax {
		f.add("interests", fmt.Sprintf("A person has at most %d interests, but %d were given", interestsMax, len(body.Interests)))
		return nil, f
	}
	out := make([]int64, 0, len(body.Interests))
	for i, in := range body.Interests {
		if slices.Contains(out, in.CompetenceId) {
			f.add(fmt.Sprintf("interests[%d].competenceId", i), fmt.Sprintf("Competence %d is named twice", in.CompetenceId))
			continue
		}
		out = append(out, in.CompetenceId)
	}
	return out, f
}

// parsedPreference is one preference as it is stored: its kind, its value in
// the kind's canonical form, its weight, and the locations a location
// preference names.
type parsedPreference struct {
	kind      string
	value     []byte
	weight    int16
	locations []int64
}

// parsePreferences judges a preferences replace: at most seven, one per kind
// (a kind sent again refused on the later index), the weight 1 to 3 and the
// value by its kind's rule.
func parsePreferences(body gen.StaffPersonPreferencesRequest) ([]parsedPreference, fieldErrors) {
	var f fieldErrors
	if len(body.Preferences) > preferencesMax {
		f.add("preferences", fmt.Sprintf("A person has at most %d preferences, one per kind, but %d were given", preferencesMax, len(body.Preferences)))
		return nil, f
	}
	out := make([]parsedPreference, 0, len(body.Preferences))
	var seen []string
	for i, p := range body.Preferences {
		field := func(name string) string { return fmt.Sprintf("preferences[%d].%s", i, name) }
		kind, msg := oneOf("A kind", string(p.Kind), preferenceKinds)
		f.add(field("kind"), msg)
		if kind != nil && slices.Contains(seen, *kind) {
			f.add(field("kind"), fmt.Sprintf("A person has one preference of the kind %s; it is given twice", *kind))
			kind = nil
		}
		if p.Weight < preferenceWeightMin || p.Weight > preferenceWeightMax {
			f.add(field("weight"), fmt.Sprintf("A weight is %d to %d", preferenceWeightMin, preferenceWeightMax))
		}
		if kind == nil {
			continue
		}
		seen = append(seen, *kind)
		raw, err := json.Marshal(p.Value)
		if err != nil {
			f.add(field("value"), "The value is not a JSON object")
			continue
		}
		value, locations, msg := canonicalPreference(*kind, raw)
		if msg != "" {
			f.add(field("value"), msg)
			continue
		}
		out = append(out, parsedPreference{kind: *kind, value: value, weight: int16(p.Weight), locations: locations})
	}
	return out, f
}

// The values of the preference kinds (S9), each decoded strictly: a key the
// kind does not take is refused.
type (
	daysValue struct {
		Days []int `json:"days"`
	}
	partsValue struct {
		Parts []string `json:"parts"`
	}
	locationsValue struct {
		LocationIDs []int64 `json:"locationIds"`
	}
	extentValue struct {
		Extent string `json:"extent"`
	}
	shareValue struct {
		Share string `json:"share"`
	}
	maxDaysValue struct {
		Days int `json:"days"`
	}
	hoursValue struct {
		Hours int `json:"hours"`
	}
)

// decodeStrict decodes raw into v, refusing a key v does not name.
func decodeStrict(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

// canonicalPreference judges raw as kind's value and answers it in its
// canonical form — the days, the parts and the locations sorted, re-encoded
// from the kind's own shape — so two equal values are equal bytes; a location
// preference's ids beside it, for the lock to judge. A message says why raw
// is no such value. A value stored before reads back through the same door.
func canonicalPreference(kind string, raw []byte) ([]byte, []int64, string) {
	var (
		value     any
		locations []int64
		msg       string
	)
	switch kind {
	case preferenceWeekdays:
		var v daysValue
		if decodeStrict(raw, &v) != nil {
			return nil, nil, `A weekdays preference is {"days": [1..7]}`
		}
		msg = distinctInRange("days", v.Days, 1, 7, 7)
		slices.Sort(v.Days)
		value = v
	case preferenceTimeOfDay:
		var v partsValue
		if decodeStrict(raw, &v) != nil {
			return nil, nil, `A time-of-day preference is {"parts": ["morning" | "day" | "evening" | "night"]}`
		}
		msg = distinctOf("parts", v.Parts, timeOfDayParts)
		slices.SortFunc(v.Parts, func(a, b string) int { return slices.Index(timeOfDayParts, a) - slices.Index(timeOfDayParts, b) })
		value = v
	case preferenceLocation:
		var v locationsValue
		if decodeStrict(raw, &v) != nil {
			return nil, nil, `A location preference is {"locationIds": [...]}`
		}
		switch {
		case len(v.LocationIDs) == 0 || len(v.LocationIDs) > locationIDsMax:
			msg = fmt.Sprintf("A location preference names 1 to %d locations", locationIDsMax)
		case hasRepeat(v.LocationIDs):
			msg = "A location is named twice"
		}
		slices.Sort(v.LocationIDs)
		locations = v.LocationIDs
		value = v
	case preferenceTravel:
		var v extentValue
		if decodeStrict(raw, &v) != nil || !slices.Contains(travelExtents, v.Extent) {
			return nil, nil, `A travel preference is {"extent": "none" | "regional" | "national" | "international"}`
		}
		value = v
	case preferenceRemote:
		var v shareValue
		if decodeStrict(raw, &v) != nil || !slices.Contains(remoteShares, v.Share) {
			return nil, nil, `A remote preference is {"share": "never" | "some" | "mostly" | "only"}`
		}
		value = v
	case preferenceMaxConsecutiveDays:
		var v maxDaysValue
		if decodeStrict(raw, &v) != nil || v.Days < 1 || v.Days > consecutiveDaysMax {
			return nil, nil, fmt.Sprintf(`A most-consecutive-days preference is {"days": 1..%d}`, consecutiveDaysMax)
		}
		value = v
	case preferenceMaxHoursPerWeek:
		var v hoursValue
		if decodeStrict(raw, &v) != nil || v.Hours < 1 || v.Hours > hoursPerWeekMax {
			return nil, nil, fmt.Sprintf(`A most-hours-a-week preference is {"hours": 1..%d}`, hoursPerWeekMax)
		}
		value = v
	default:
		return nil, nil, fmt.Sprintf("%s is no preference kind", kind)
	}
	if msg != "" {
		return nil, nil, msg
	}
	out, err := json.Marshal(value)
	if err != nil {
		return nil, nil, "The value could not be encoded"
	}
	return out, locations, ""
}

// distinctInRange is the message for a list of 1 to most distinct values from
// lo to hi, "" when it is one.
func distinctInRange(name string, values []int, lo, hi, most int) string {
	if len(values) == 0 || len(values) > most {
		return fmt.Sprintf("The %s are 1 to %d values", name, most)
	}
	if hasRepeat(values) {
		return fmt.Sprintf("The %s name a value twice", name)
	}
	for _, v := range values {
		if v < lo || v > hi {
			return fmt.Sprintf("The %s are %d to %d", name, lo, hi)
		}
	}
	return ""
}

// distinctOf is the message for a list of 1 to len(allowed) distinct values
// of allowed, "" when it is one.
func distinctOf(name string, values, allowed []string) string {
	if len(values) == 0 || len(values) > len(allowed) {
		return fmt.Sprintf("The %s are 1 to %d values", name, len(allowed))
	}
	if hasRepeat(values) {
		return fmt.Sprintf("The %s name a value twice", name)
	}
	for _, v := range values {
		if !slices.Contains(allowed, v) {
			return fmt.Sprintf("The %s are of %v", name, allowed)
		}
	}
	return ""
}

// hasRepeat reports whether values names one value twice.
func hasRepeat[T comparable](values []T) bool {
	seen := make(map[T]bool, len(values))
	for _, v := range values {
		if seen[v] {
			return true
		}
		seen[v] = true
	}
	return false
}

// declarationKey is one availability row by every field it has, for telling
// two rows equal: a nil weekday or minute is -1, an absent day "".
type declarationKey struct {
	kind                string
	weekday, start, end int16
	from, to            string
}

// parsedDeclaration is one availability row as it is stored.
type parsedDeclaration struct {
	kind                string
	weekday, start, end *int16
	from, to            pgtype.Date
}

func (d parsedDeclaration) key() declarationKey {
	small := func(v *int16) int16 {
		if v == nil {
			return -1
		}
		return *v
	}
	day := func(d pgtype.Date) string {
		if !d.Valid {
			return ""
		}
		return d.Time.Format(time.DateOnly)
	}
	return declarationKey{kind: d.kind, weekday: small(d.weekday), start: small(d.start), end: small(d.end), from: day(d.from), to: day(d.to)}
}

// parseDeclarations judges an availability replace (S10): at most fifty
// rows; the kind of the three; a weekday 1 to 7, or none and then a first
// day; a last day on or after the first; the minutes both or neither, 0 to
// 1440, the end after the start; a row equal in every field to one before it
// refused on its own index.
func parseDeclarations(body gen.StaffAvailabilityRequest) ([]parsedDeclaration, fieldErrors) {
	var f fieldErrors
	if len(body.Declarations) > declarationsMax {
		f.add("declarations", fmt.Sprintf("A declaration has at most %d rows, but %d were given", declarationsMax, len(body.Declarations)))
		return nil, f
	}
	out := make([]parsedDeclaration, 0, len(body.Declarations))
	var keys []declarationKey
	for i, d := range body.Declarations {
		field := func(name string) string { return fmt.Sprintf("declarations[%d].%s", i, name) }
		var (
			row  parsedDeclaration
			fail bool
		)
		add := func(name, msg string) { f.add(field(name), msg); fail = true }
		kind, msg := oneOf("A kind", string(d.Kind), availabilityKind)
		if msg != "" {
			add("kind", msg)
		} else {
			row.kind = *kind
		}
		if d.Weekday != nil && (*d.Weekday < 1 || *d.Weekday > 7) {
			add("weekday", "A weekday is 1 (Monday) to 7 (Sunday)")
		}
		if d.Weekday == nil && d.FromDate == nil {
			add("fromDate", "A row without a weekday is dated, and needs its first day")
		}
		if d.FromDate != nil && d.ToDate != nil && d.ToDate.Before(d.FromDate.Time) {
			add("toDate", "The last day is on or after the first")
		}
		switch {
		case d.StartMinute != nil && d.EndMinute == nil:
			add("endMinute", "A row with a start minute needs its end minute too")
		case d.StartMinute == nil && d.EndMinute != nil:
			add("startMinute", "A row with an end minute needs its start minute too")
		case d.StartMinute != nil:
			start, end := *d.StartMinute, *d.EndMinute
			if start < 0 || start > minutesPerDay {
				add("startMinute", fmt.Sprintf("A minute of the day is 0 to %d", minutesPerDay))
			}
			if end < 0 || end > minutesPerDay {
				add("endMinute", fmt.Sprintf("A minute of the day is 0 to %d", minutesPerDay))
			} else if end <= start {
				add("endMinute", "The end minute is after the start minute")
			}
		}
		if fail {
			continue
		}
		row.weekday, row.start, row.end = smallOf(d.Weekday), smallOf(d.StartMinute), smallOf(d.EndMinute)
		row.from, row.to = pgDate(d.FromDate), pgDate(d.ToDate)
		k := row.key()
		if slices.Contains(keys, k) {
			f.add(fmt.Sprintf("declarations[%d]", i), "This row is given twice")
			continue
		}
		keys = append(keys, k)
		out = append(out, row)
	}
	return out, f
}

// smallOf is an optional contract integer as a smallint parameter; the
// rules have bounded it already.
func smallOf(v *int32) *int16 {
	if v == nil {
		return nil
	}
	s := int16(*v)
	return &s
}
