package staff

import (
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/staff/gen"
	"github.com/vantigo-io/vantigo/server/internal/staff/store"
)

// This file is what delivery B adds to the settings and to the person (plan
// Task 2): the self-certification policy and automatic carry-over on the
// settings row (design §3.6, plan reading 9), and the care-day fields on the
// person (design S11, A1 reading 19), each parsed into what the write stores.

// The self-certification policy's presets (design S10, research §4.5): the
// statutory minimum — 3 calendar days an occurrence, 4 occurrences in a
// rolling year — and the extended one an IA-style agreement gives — 8 days
// an occurrence, 24 days in a rolling year. Under a preset the counters are
// the preset's whatever the request carries; the one a preset does not use
// keeps what it holds, and custom takes them as sent.
const (
	selfCertMinimum  = "minimum"
	selfCertExtended = "extended"
	selfCertCustom   = "custom"
)

// The bounds of the policy's numbers (plan "Exact values").
const (
	selfCertDaysPerOccurrenceMax  = 16
	selfCertOccurrencesPerYearMax = 52
	selfCertDaysPerYearMax        = 365
	selfCertAfterMonthsMax        = 2
)

// selfCertPolicy is the policy part of the settings row, as stored.
type selfCertPolicy struct {
	Policy                                string
	DaysPerOccurrence, OccurrencesPerYear int16
	DaysPerYear, AfterMonths              int16
	AutoCarryOver                         bool
	AutoCarryOverSince                    pgtype.Date
}

// policyOf is the policy part of a settings row.
func policyOf(row store.StaffSetting) selfCertPolicy {
	return selfCertPolicy{
		Policy: row.SelfCertPolicy, DaysPerOccurrence: row.SelfCertDaysPerOccurrence,
		OccurrencesPerYear: row.SelfCertOccurrencesPerYear, DaysPerYear: row.SelfCertDaysPerYear,
		AfterMonths: row.SelfCertAfterMonths, AutoCarryOver: row.AutoCarryOver, AutoCarryOverSince: row.AutoCarryOverSince,
	}
}

// boundedSmall is an optional number within [low, high], or the message
// refusing it; nil when left out.
func boundedSmall(what string, v *int32, low, high int) (*int16, string) {
	if v == nil {
		return nil, ""
	}
	if *v < int32(low) || *v > int32(high) {
		return nil, fmt.Sprintf("%s is %d to %d", what, low, high)
	}
	out := int16(*v)
	return &out, ""
}

// parsedPolicy is the policy fields of a settings body, judged: each left
// out is nil, and keeps what the row holds.
type parsedPolicy struct {
	Policy                                *string
	DaysPerOccurrence, OccurrencesPerYear *int16
	DaysPerYear, AfterMonths              *int16
	AutoCarryOver                         *bool
}

// parsePolicy judges the policy fields of a settings body, every failure
// collected on f. The policy's name is the contract's enum.
func parsePolicy(body gen.StaffSettingsRequest, f *fieldErrors) parsedPolicy {
	out := parsedPolicy{Policy: stringOf(body.SelfCertPolicy), AutoCarryOver: body.AutoCarryOver}
	var msg string
	_, msg = parseEnum("A self-certification policy", out.Policy, []string{selfCertMinimum, selfCertExtended, selfCertCustom})
	f.add("selfCertPolicy", msg)
	out.DaysPerOccurrence, msg = boundedSmall("The days of one occurrence", body.SelfCertDaysPerOccurrence, 1, selfCertDaysPerOccurrenceMax)
	f.add("selfCertDaysPerOccurrence", msg)
	out.OccurrencesPerYear, msg = boundedSmall("The occurrences in a rolling year", body.SelfCertOccurrencesPerYear, 1, selfCertOccurrencesPerYearMax)
	f.add("selfCertOccurrencesPerYear", msg)
	out.DaysPerYear, msg = boundedSmall("The self-certified days in a rolling year", body.SelfCertDaysPerYear, 1, selfCertDaysPerYearMax)
	f.add("selfCertDaysPerYear", msg)
	out.AfterMonths, msg = boundedSmall("The months of employment before egenmelding", body.SelfCertAfterMonths, 0, selfCertAfterMonthsMax)
	f.add("selfCertAfterMonths", msg)
	return out
}

// applyTo is the policy the row holds with p laid over it, judged on today:
// a preset writes its own counters, custom the ones sent, and turning
// automatic carry-over on — off before, on now — records today as the day it
// was turned on (plan pre-flight M6); turning it off keeps that day.
func (p parsedPolicy) applyTo(current selfCertPolicy, today time.Time) selfCertPolicy {
	next := current
	if p.Policy != nil {
		next.Policy = *p.Policy
	}
	switch next.Policy {
	case selfCertMinimum:
		next.DaysPerOccurrence, next.OccurrencesPerYear = 3, 4
	case selfCertExtended:
		next.DaysPerOccurrence, next.DaysPerYear = 8, 24
	default:
		for _, f := range []struct {
			in  *int16
			out *int16
		}{
			{p.DaysPerOccurrence, &next.DaysPerOccurrence}, {p.OccurrencesPerYear, &next.OccurrencesPerYear},
			{p.DaysPerYear, &next.DaysPerYear},
		} {
			if f.in != nil {
				*f.out = *f.in
			}
		}
	}
	if p.AfterMonths != nil {
		next.AfterMonths = *p.AfterMonths
	}
	if p.AutoCarryOver != nil {
		if *p.AutoCarryOver && !current.AutoCarryOver {
			next.AutoCarryOverSince = dayParam(today)
		}
		next.AutoCarryOver = *p.AutoCarryOver
	}
	return next
}

// The care-day fields' bounds (plan "Exact values").
const careDayChildrenMax = 20

// careDays is the person's care-day fields as stored: nil is "not recorded".
type careDays struct {
	ChildrenUnder12        *int16
	SoleCarer              *bool
	ChronicallyIllChildren *int16
}

// parseCareDays judges the care-day fields of a person's body, every failure
// collected on f. Left out, a field is cleared, as every optional field of
// the full replace is.
func parseCareDays(body gen.StaffPersonRequest, f *fieldErrors) careDays {
	var out careDays
	var msg string
	out.ChildrenUnder12, msg = boundedSmall("The children under twelve", body.ChildrenUnder12, 0, careDayChildrenMax)
	f.add("childrenUnder12", msg)
	out.ChronicallyIllChildren, msg = boundedSmall("The chronically ill or disabled children", body.ChronicallyIllChildren, 0, careDayChildrenMax)
	f.add("chronicallyIllChildren", msg)
	out.SoleCarer = body.SoleCarer
	return out
}
