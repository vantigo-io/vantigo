package staff

import (
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is an absence's rules (plan "Exact values"): the ones a request
// carries on its own, judged before any lock, and the ones its type decides,
// judged under the person's lock on the type then read.

// The bounds of an absence (plan "Exact values").
const (
	absenceNoteMaxLength = 500
	absenceMaxDays       = 366
	quarterHour          = 25   // hundredths of an hour
	minAbsenceHours      = 25   // 0.25 hours
	maxAbsenceHours      = 2400 // 24 hours
)

// The refusal titles of absence's writes.
const (
	invalidAbsenceTitle    = "Invalid absence"
	invalidAdjustmentTitle = "Invalid holiday adjustment"
	invalidCarryOverTitle  = "Invalid carry-over"
	invalidAccountTitle    = "Invalid holiday account"
)

// parsedAbsence is one validated absence: its type, its days, and its
// percent, hours, degree and note as the columns store them. HasPercent,
// HasHours and HasDegree say what the request named, for the type's rules.
type parsedAbsence struct {
	TypeID                 int64
	First, Last            time.Time
	LastGiven              bool
	Percent, Hours, Degree pgtype.Numeric
	HasPercent, HasDegree  bool
	HasHours               bool
	Note                   *string
}

// absenceFields is what a request for an absence carries, whichever schema
// it came in.
type absenceFields struct {
	TypeID                        int64
	FirstDay                      openapi_types.Date
	LastDay                       *openapi_types.Date
	Percent, Hours, DegreePercent *float64
	Note                          *string
}

func fieldsOfRequest(b gen.StaffAbsenceRequest) absenceFields {
	return absenceFields{b.TypeId, b.FirstDay, b.LastDay, b.Percent, b.Hours, b.DegreePercent, b.Note}
}

func fieldsOfChange(b gen.StaffAbsenceChangeRequest) absenceFields {
	return absenceFields{b.TypeId, b.FirstDay, b.LastDay, b.Percent, b.Hours, b.DegreePercent, b.Note}
}

// parseAbsence runs the rules a request carries on its own: the last day on
// or after the first and the range at most 366 days; a percent and a degree
// of 1 to 100 with two decimals; hours of 0.25 to 24 in quarter hours; a
// note of at most 500 characters.
func parseAbsence(in absenceFields) (parsedAbsence, fieldErrors) {
	var f fieldErrors
	out := parsedAbsence{TypeID: in.TypeID, First: in.FirstDay.Time, Last: in.FirstDay.Time, Percent: numericOfHundredths(wholeDay)}
	if in.LastDay != nil {
		out.Last, out.LastGiven = in.LastDay.Time, true
	}
	switch {
	case out.Last.Before(out.First):
		f.add("lastDay", "The last day cannot be before the first")
	case calendarDays(out.First, out.Last) > absenceMaxDays:
		f.add("lastDay", fmt.Sprintf("An absence is at most %d days; register a longer one as more than one", absenceMaxDays))
	}
	if in.Percent != nil {
		out.HasPercent = true
		h, ok := hundredthsOf(*in.Percent)
		if !ok || h < 100 || h > wholeDay {
			f.add("percent", "A percentage is 1 to 100 with at most two decimals")
		} else {
			out.Percent = numericOfHundredths(h)
		}
	}
	if in.DegreePercent != nil {
		out.HasDegree = true
		h, ok := hundredthsOf(*in.DegreePercent)
		if !ok || h < 100 || h > wholeDay {
			f.add("degreePercent", "A degree is 1 to 100 with at most two decimals")
		} else {
			out.Degree = numericOfHundredths(h)
		}
	}
	if in.Hours != nil {
		out.HasHours = true
		h, ok := hundredthsOf(*in.Hours)
		if !ok || h < minAbsenceHours || h > maxAbsenceHours || h%quarterHour != 0 {
			f.add("hours", "Hours are 0.25 to 24 in quarter hours")
		} else {
			out.Hours = numericOfHundredths(h)
		}
	}
	var msg string
	out.Note, msg = optionalText("A note", in.Note, absenceNoteMaxLength)
	f.add("note", msg)
	return out, f
}

// judgeType runs the rules the type decides (design S8, §3.5, plan pre-flight
// I4): a type to pick (not hidden, so never the reserved absent); a
// percentage only on a type that allows one (percent_not_allowed) — never
// on a sick kind, whose share is its degree; hours on an hour type, which
// needs them and one day, and nowhere else (hours_on_day_type); no note on a
// sick or child-sick type (absence_note_on_sick); a degree on a sykmelding
// only, 100 when it names none. It answers p with the degree filled in.
func judgeType(t store.StaffAbsenceType, p parsedAbsence, f *fieldErrors) parsedAbsence {
	if t.Hidden {
		f.add("typeId", fmt.Sprintf("%s is not a type to register an absence of", t.NameEn))
		return p
	}
	if p.HasPercent && !t.AllowsPercent {
		f.addCoded("percent", codePercentNotAllowed, fmt.Sprintf("%s takes no percentage", t.NameEn))
	}
	hasHours := p.HasHours
	switch {
	case t.Unit == unitDays && hasHours:
		f.addCoded("hours", codeHoursOnDayType, fmt.Sprintf("%s is counted in days and takes no hours", t.NameEn))
	case t.Unit == unitHours && !hasHours:
		f.addCoded("hours", codeHoursOnDayType, fmt.Sprintf("%s is counted in hours: say how many", t.NameEn))
	case t.Unit == unitHours && !p.Last.Equal(p.First):
		f.addCoded("lastDay", codeHoursOnDayType, fmt.Sprintf("%s is the hours of one day", t.NameEn))
	}
	if p.Note != nil && healthKind(t.Kind) {
		f.addCoded("note", codeAbsenceNoteOnSick, "A sick or child-sick absence carries no note: what it is about is health data")
	}
	if p.HasDegree && t.Kind != kindSickCertified {
		f.add("degreePercent", "Only a sykmelding has a degree")
	}
	if t.Kind == kindSickCertified && !p.HasDegree {
		p.Degree = numericOfHundredths(wholeDay)
	}
	if t.Unit == unitDays {
		p.Hours = pgtype.Numeric{}
	}
	return p
}

// withinEmployment is whether first..last lies inside one of employments.
func withinEmployment(employments []store.StaffEmployment, first, last time.Time) bool {
	for _, e := range employments {
		if !civil(e.StartDate).After(first) && (!e.EndDate.Valid || !e.EndDate.Time.Before(last)) {
			return true
		}
	}
	return false
}

// tenthsOf is a JSON number with at most one decimal as tenths.
func tenthsOf(v float64) (int64, bool) {
	t := math.Round(v * 10)
	if math.Abs(v*10-t) > 1e-6 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	return int64(t), true
}

// numericOfTenths stores tenths exactly, as a numeric(_,1).
func numericOfTenths(t int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(t), Exp: -1, Valid: true}
}
