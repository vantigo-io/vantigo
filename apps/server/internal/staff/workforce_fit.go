package staff

import (
	"context"
	"fmt"
	"slices"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// PreferenceFits is the matching contract's Staff side (competence design
// S15, reading 36; planning design S17.6): for staff:view, each of personIDs
// the Colleague rule shows over the booking's period, with how the booking
// fits what they want — a score that starts at 100 and loses weight × 10 for
// each preference the booking contradicts, and the kinds lowered, never a
// value. A person with no preference scores 100. A booking field left nil or
// empty contradicts nothing (contradicts). Nothing for anyone else; a period
// outside the bound is ErrWindowOutOfBounds.
func (w *workforce) PreferenceFits(ctx context.Context, personIDs []int64, b contracts.Booking) (map[int64]contracts.Fit, error) {
	if !contracts.HasPermission(ctx, w.access, permView) {
		return nil, nil
	}
	win, err := w.window(ctx, b.From, b.To)
	if err != nil {
		return nil, err
	}
	out := map[int64]contracts.Fit{}
	if len(personIDs) == 0 {
		return out, nil
	}
	_, ids, err := w.windowPeople(ctx, win, personIDs)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := w.q.WorkforcePreferences(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("staff: read the preferences for the fit: %w", err)
	}
	for _, id := range ids {
		out[id] = contracts.Fit{Score: 100}
	}
	for _, r := range rows {
		if !contradicts(r.Kind, r.Value, b) {
			continue
		}
		f := out[r.PersonID]
		f.Score -= int(r.Weight) * 10
		if f.Score < 0 {
			f.Score = 0
		}
		f.Lowered = append(f.Lowered, r.Kind)
		out[r.PersonID] = f
	}
	for id, f := range out {
		slices.SortFunc(f.Lowered, func(a, b string) int {
			return slices.Index(preferenceKinds, a) - slices.Index(preferenceKinds, b)
		})
		out[id] = f
	}
	return out, nil
}

// contradicts is whether the booking contradicts one stored preference of
// kind with the canonical value (the Staff reference "The matching
// contract's Staff side"): weekdays — a weekday of the booking the person
// does not prefer; time_of_day — a part of the day the booking has that the
// person does not prefer; location — the booking's location not among the
// person's; travel — the booking's extent beyond the person's (none <
// regional < national < international); remote — the booking's share other
// than the person's; max_consecutive_days and max_hours_per_week — the
// booking's figure above the person's. The booking's facet left nil or empty
// contradicts nothing, and a value that does not decode is no contradiction
// either: the fit never fails on a stored row.
func contradicts(kind string, value []byte, b contracts.Booking) bool {
	switch kind {
	case preferenceWeekdays:
		var v daysValue
		if len(b.Weekdays) == 0 || decodeStrict(value, &v) != nil {
			return false
		}
		return slices.ContainsFunc(b.Weekdays, func(d int) bool { return !slices.Contains(v.Days, d) })
	case preferenceTimeOfDay:
		var v partsValue
		if len(b.Parts) == 0 || decodeStrict(value, &v) != nil {
			return false
		}
		return slices.ContainsFunc(b.Parts, func(p string) bool { return !slices.Contains(v.Parts, p) })
	case preferenceLocation:
		var v locationsValue
		if b.LocationID == nil || decodeStrict(value, &v) != nil {
			return false
		}
		return !slices.Contains(v.LocationIDs, *b.LocationID)
	case preferenceTravel:
		var v extentValue
		if b.Travel == "" || decodeStrict(value, &v) != nil {
			return false
		}
		return slices.Index(travelExtents, b.Travel) > slices.Index(travelExtents, v.Extent)
	case preferenceRemote:
		var v shareValue
		if b.Remote == "" || decodeStrict(value, &v) != nil {
			return false
		}
		return b.Remote != v.Share
	case preferenceMaxConsecutiveDays:
		var v maxDaysValue
		if b.ConsecutiveDays == nil || decodeStrict(value, &v) != nil {
			return false
		}
		return *b.ConsecutiveDays > v.Days
	case preferenceMaxHoursPerWeek:
		var v hoursValue
		if b.HoursPerWeek == nil || decodeStrict(value, &v) != nil {
			return false
		}
		return *b.HoursPerWeek > float64(v.Hours)
	}
	return false
}
