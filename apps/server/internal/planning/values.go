package planning

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/holidays"
)

// This file is the module's value rules and conversions: civil dates (rule 1:
// dates are civil and compared at midnight UTC), weeks (Monday to Sunday,
// S6), the two-decimal load, and the If-Match revision.

// civil is a date column's calendar day, the zero time for NULL.
func civil(d pgtype.Date) time.Time {
	if !d.Valid {
		return time.Time{}
	}
	return d.Time
}

// dayParam is a calendar day as a date parameter.
func dayParam(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

// civilToday is the calendar day of now in zone, as a date at midnight UTC:
// the platform's one derivation of a day from an instant (holidays.DayIn).
func civilToday(now time.Time, zone *time.Location) time.Time {
	return holidays.DayIn(now, zone)
}

// dayOf is a contract date as a civil date at midnight UTC.
func dayOf(d openapi_types.Date) time.Time {
	y, m, day := d.Date()
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}

// apiDate is a civil date as a contract date.
func apiDate(t time.Time) openapi_types.Date { return openapi_types.Date{Time: t} }

// addDays is d moved n days, at midnight UTC: day arithmetic on a UTC
// midnight never meets a daylight-saving shift.
func addDays(d time.Time, n int) time.Time { return d.AddDate(0, 0, n) }

// mondayOf is the Monday of d's week (ISO 8601: weeks run Monday to Sunday).
func mondayOf(d time.Time) time.Time {
	back := (int(d.Weekday()) + 6) % 7 // Monday 0 … Sunday 6
	return addDays(d, -back)
}

// sundayOf is the Sunday that ends d's week.
func sundayOf(d time.Time) time.Time { return addDays(mondayOf(d), 6) }

// mondaysOf is the Monday of every week from..to touches, in order: the
// columns of the board and the weeks of a plan. None when to is before from.
func mondaysOf(from, to time.Time) []time.Time {
	var out []time.Time
	for m := mondayOf(from); !m.After(to); m = addDays(m, 7) {
		out = append(out, m)
	}
	return out
}

// hundredthsOf is a JSON number with at most two decimals as hundredths, and
// false for one with more: the numeric(5,2) load holds what was typed, never
// a rounding of it (S2).
func hundredthsOf(v float64) (int64, bool) {
	h := math.Round(v * 100)
	if math.Abs(v*100-h) > 1e-6 || math.IsInf(v, 0) || math.IsNaN(v) {
		return 0, false
	}
	return int64(h), true
}

// numericOfHundredths stores hundredths exactly: an integer with a scale of
// two, never through a float.
func numericOfHundredths(h int64) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(h), Exp: -2, Valid: true}
}

// hundredthsOfNumeric reads a numeric(_,2) column back as hundredths; NULL is
// 0.
func hundredthsOfNumeric(n pgtype.Numeric) int64 {
	if !n.Valid || n.Int == nil {
		return 0
	}
	v := new(big.Int).Set(n.Int)
	for e := n.Exp + 2; e > 0; e-- {
		v.Mul(v, big.NewInt(10))
	}
	for e := n.Exp + 2; e < 0; e++ {
		v.Quo(v, big.NewInt(10))
	}
	return v.Int64()
}

// loadOf is a load column as the number the contract carries.
func loadOf(n pgtype.Numeric) float64 { return float64(hundredthsOfNumeric(n)) / 100 }

// roundHours is an hour figure to the hundredth, the precision the contract
// answers every figure in, so a sum of fifths reads 8 and not 7.999999.
func roundHours(v float64) float64 { return math.Round(v*100) / 100 }

// parseIfMatch reads the revision an If-Match header carries: the bare number
// the response's revision gave, or that number quoted — and weakly tagged — as
// a client that treats the header as an ETag sends it back. A revision is 1 or
// more; anything else is no revision at all, which the caller answers as a
// 400.
func parseIfMatch(raw string) (int32, bool) {
	v := strings.TrimSpace(raw)
	v = strings.TrimPrefix(v, "W/")
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < 1 {
		return 0, false
	}
	return int32(n), true
}

// badIfMatch is the field error an If-Match that carries no revision answers.
// The header is required by the contract, so a missing one never reaches a
// handler: the generated wrapper refuses it first, as a bare 400.
func badIfMatch() fieldErrors {
	return fieldErrors{errs: fieldError("If-Match", "If-Match must carry the revision the row was read at, such as \"3\"")}
}
