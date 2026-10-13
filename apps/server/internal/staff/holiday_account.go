package staff

import "time"

// This file is the holiday account's arithmetic (design S9, §4 The holiday
// account), pure: the conversion of a scheme's virkedager to working days,
// and the account of one person for one year from their employments, their
// schemes, their absences of the types that draw on it and their
// adjustments. holiday.go reads the rows and answers it.

// convertVirkedager is v virkedager — Monday to Saturday, as the Holiday Act
// counts — as working days of a five-day week, the way Norwegian payroll
// converts them: ⌊v / 6⌋ whole weeks of five days, and the rest up to five.
// 25 virkedager are 21 days, 30 are 25, 31 are 26 (plan pre-flight I3), so a
// Monday-to-Friday week costs five days and an entitlement is never
// undercounted (design M6).
func convertVirkedager(v int) int {
	return v/6*5 + min(v%6, 5)
}

// dayUnit is one working day in the account's unit: ten-thousandths of a
// day, so a share in hundredths of a percent times whole working days is
// exact, and an adjustment's tenths are too.
const dayUnit = 10000

// accountScheme is a scheme's figures in virkedager, as the account reads
// them.
type accountScheme struct{ Entitlement, ExtraFrom60, AfterSeptember, MaxCarryOver int }

// accountEmployment is an employment's span and the scheme it follows (the
// default when it names none). End is nil while open-ended.
type accountEmployment struct {
	Start  time.Time
	End    *time.Time
	Scheme accountScheme
}

// accountAbsence is a day row of a type that draws on the account: its days,
// its status and its share of each day (shareOf).
type accountAbsence struct {
	First, Last time.Time
	Status      string
	Share       int64
}

// accountAdjustment is one holiday adjustment: the year it changes, its days
// in tenths, its reason and — for a carry-over — the year it comes from.
type accountAdjustment struct {
	Year     int
	Tenths   int64
	Reason   string
	FromYear *int
}

// accountInput is everything a person's account is derived from.
// AutoCarryOverSince is the day automatic carry-over was turned on, nil while
// it is off.
type accountInput struct {
	BirthDate          *time.Time
	Employments        []accountEmployment
	Absences           []accountAbsence
	Adjustments        []accountAdjustment
	AutoCarryOverSince *time.Time
}

// holidayAccount is one year's account, every figure in dayUnit.
// Overdrawn is whether remaining less pending is below zero: taking every
// requested day would overdraw it — a warning, never a refusal (§4).
type holidayAccount struct {
	Year                                                                int
	Entitlement, CarriedIn, CarriedInDerived, Taken, Pending, Remaining int64
	Overdrawn                                                           bool
}

// The adjustments' reasons (design §3.5).
const (
	reasonAgreedCarryOver = "agreed_carry_over"
	reasonForcedCarryOver = "forced_carry_over"
	reasonCorrection      = "correction"
)

// carryOverReason is whether reason is a carry-over, which names the year it
// comes from and is recorded at most once per person and that year.
func carryOverReason(reason string) bool {
	return reason == reasonAgreedCarryOver || reason == reasonForcedCarryOver
}

// accountFor is the person's holiday account for year (design §4):
//
//   - entitlement: the scheme of the earliest employment active in the year
//     (§9: two concurrent employments, one account), converted — its
//     after-September days when that employment starts after 30 September of
//     the year — plus the extra days converted when the person turns sixty
//     or more in the year, un-prorated (spec reading 5); nothing without an
//     employment in the year;
//   - carried in: the year's agreed carry-overs, capped at the scheme's
//     maximum converted, and its forced carry-overs and corrections uncapped;
//   - derived carry-in: with automatic carry-over on, for a year from the one
//     it was turned on, when no carry-over from the year before is recorded,
//     that year's positive remaining (plan pre-flight M6);
//   - taken and pending: the working days of the year of the approved or
//     registered, and of the requested, rows, each times its share — a row
//     across New Year counts in both years (§9);
//   - remaining: entitlement and both carry-ins less taken.
func accountFor(in accountInput, year int) holidayAccount {
	out := holidayAccount{Year: year}
	jan1 := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	dec31 := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	earliest := earliestEmployment(in, year)
	if earliest != nil {
		v := earliest.Scheme.Entitlement
		if earliest.Start.After(time.Date(year, time.September, 30, 0, 0, 0, 0, time.UTC)) {
			v = earliest.Scheme.AfterSeptember
		}
		out.Entitlement = int64(convertVirkedager(v)) * dayUnit
		if in.BirthDate != nil && year-in.BirthDate.Year() >= 60 {
			out.Entitlement += int64(convertVirkedager(earliest.Scheme.ExtraFrom60)) * dayUnit
		}
	}

	var agreed int64
	recordedFromLastYear := false
	for _, a := range in.Adjustments {
		if carryOverReason(a.Reason) && a.FromYear != nil && *a.FromYear == year-1 {
			recordedFromLastYear = true
		}
		if a.Year != year {
			continue
		}
		units := a.Tenths * (dayUnit / 10)
		if a.Reason == reasonAgreedCarryOver {
			agreed += units
		} else {
			out.CarriedIn += units
		}
	}
	if agreed != 0 {
		limit := agreed
		if earliest != nil {
			limit = min(agreed, int64(convertVirkedager(earliest.Scheme.MaxCarryOver))*dayUnit)
		}
		out.CarriedIn += limit
	}

	if s := in.AutoCarryOverSince; s != nil && year >= s.Year() && !recordedFromLastYear {
		if last := accountFor(in, year-1); last.Remaining > 0 {
			out.CarriedInDerived = last.Remaining
		}
	}

	for _, a := range in.Absences {
		first, last := maxTime(a.First, jan1), minTime(a.Last, dec31)
		units := int64(workingDays(first, last)) * a.Share
		switch a.Status {
		case absenceApproved, absenceRegistered:
			out.Taken += units
		case absenceRequested:
			out.Pending += units
		}
	}
	out.Remaining = out.Entitlement + out.CarriedIn + out.CarriedInDerived - out.Taken
	out.Overdrawn = out.Remaining-out.Pending < 0
	return out
}

// maxTime and minTime are the later and the earlier of two days.
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// earliestEmployment is the person's earliest employment active in year —
// the one whose scheme the year's account follows (§9) — nil when none is.
func earliestEmployment(in accountInput, year int) *accountEmployment {
	jan1 := time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC)
	dec31 := time.Date(year, time.December, 31, 0, 0, 0, 0, time.UTC)
	var earliest *accountEmployment
	for i, e := range in.Employments {
		if e.Start.After(dec31) || (e.End != nil && e.End.Before(jan1)) {
			continue
		}
		if earliest == nil || e.Start.Before(earliest.Start) {
			earliest = &in.Employments[i]
		}
	}
	return earliest
}

// carryOverCap is the most an agreed carry-over from fromYear brings into the
// year after, in dayUnit: the scheme's maximum converted (design §4) — the
// scheme the year after's account follows, else fromYear's — and -1 when the
// person had no employment in either.
func carryOverCap(in accountInput, fromYear int) int64 {
	e := earliestEmployment(in, fromYear+1)
	if e == nil {
		e = earliestEmployment(in, fromYear)
	}
	if e == nil {
		return -1
	}
	return int64(convertVirkedager(e.Scheme.MaxCarryOver)) * dayUnit
}
