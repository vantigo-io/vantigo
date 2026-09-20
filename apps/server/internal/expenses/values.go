package expenses

import (
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file is the module's value rules and the conversions between the wire's
// numbers and dates and the columns'. Each rule takes the raw value and answers
// the normalized one plus the message to report, "" when the rule holds; a
// parse function runs them all and collects every failure into a map keyed by
// the camelCase JSON path, so a caller sees every problem with their body at
// once.

// currencyPattern is an ISO 4217 alphabetic code's shape, as projects checks a
// project's currency; the set of codes itself is not enforced.
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// The ceilings the numeric columns hold. A rate is numeric(10,2), the receipt
// threshold numeric(12,2), and a markup numeric(6,2) capped at the 1000 % of
// Global Constraints.
const (
	maxRateValue     = 99_999_999.99
	maxMoney         = 9_999_999_999.99
	maxMarkupPercent = 1000.0
	maxPercent       = 100.0
)

// categoryNameMaxLength and rateSourceMaxLength are the two name columns'
// widths, in characters — what varchar(n) counts.
const (
	categoryNameMaxLength = 100
	rateSourceMaxLength   = 100
)

// withFieldError adds one message to a map another rule may already have put
// something in. A nil map is the "nothing failed yet" case, so it is grown
// rather than written to.
func withFieldError(errs map[string][]string, field, message string) map[string][]string {
	if errs == nil {
		errs = map[string][]string{}
	}
	errs[field] = append(errs[field], message)
	return errs
}

// ptrTo is a pointer to a copy of v, for the optional columns and wire fields
// that are absent rather than zero.
func ptrTo[T any](v T) *T { return &v }

// validateCurrency is the three-letter ISO 4217 rule. It answers the
// upper-cased code and the message to report, "" when it holds.
func validateCurrency(raw string) (string, string) {
	currency := strings.ToUpper(strings.TrimSpace(raw))
	switch {
	case currency == "":
		return "", "A currency is required"
	case !currencyPattern.MatchString(currency):
		return "", fmt.Sprintf("A currency must be a three-letter ISO 4217 code, but was '%s'", raw)
	}
	return currency, ""
}

// validateDecimal is the shared rule for every amount on the wire: a number
// between min and max with at most two decimals, judged on its shortest decimal
// text — the same text the numeric columns store — rather than on float
// arithmetic. label names the value in the message ("A rate").
func validateDecimal(label string, v float64, minimum, maximum float64) string {
	return validateScaled(label, v, minimum, maximum, moneyPlaces)
}

// validateScaled is validateDecimal for a column that holds a different number
// of decimals — a distance in kilometres is numeric(8,1), so a second decimal
// is a mistake worth reporting rather than something the column would round
// away behind the caller's back.
func validateScaled(label string, v float64, minimum, maximum float64, places int) string {
	switch {
	case math.IsNaN(v) || v < minimum:
		if minimum == 0 {
			return label + " cannot be negative"
		}
		return fmt.Sprintf("%s must be greater than %s", label, formatNumber(minimum))
	case v > maximum:
		return fmt.Sprintf("%s cannot be more than %s", label, formatNumber(maximum))
	}
	text := strconv.FormatFloat(v, 'f', -1, 64)
	if _, decimals, ok := strings.Cut(text, "."); ok && len(decimals) > places {
		return fmt.Sprintf("%s can have at most %s, but was %s", label, decimalPlaces(places), text)
	}
	return ""
}

// decimalPlaces names a scale in the message validateScaled reports.
func decimalPlaces(places int) string {
	if places == 1 {
		return "one decimal"
	}
	return fmt.Sprintf("%d decimals", places)
}

// ratFromFloat is a JSON number as the exact decimal it was written as, through
// its shortest round-tripping text — the way every amount reaches money.go, so
// no float arithmetic ever stands between a request and an amount.
func ratFromFloat(v float64) *big.Rat {
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(v, 'f', -1, 64))
	if !ok {
		return new(big.Rat)
	}
	return r
}

// numericFromRat stores an exact decimal as the column's value, at places
// decimals and rounded half up — the one road from money.go's arithmetic into
// the database.
func numericFromRat(v *big.Rat, places int) (pgtype.Numeric, error) {
	return numericFromText(decimalText(v, places))
}

// numericFromRatPtr is numericFromRat for an optional value; nil stays an
// invalid (SQL NULL) pgtype.Numeric.
func numericFromRatPtr(v *big.Rat, places int) (pgtype.Numeric, error) {
	if v == nil {
		return pgtype.Numeric{}, nil
	}
	return numericFromRat(v, places)
}

// ratPtrFromNumeric reads an optional numeric column as an exact decimal, nil
// for a SQL NULL.
func ratPtrFromNumeric(n pgtype.Numeric) (*big.Rat, error) {
	if !n.Valid {
		return nil, nil
	}
	return ratFromNumeric(n)
}

// validateAboveZero is validateDecimal for a value that must be greater than
// zero rather than at least zero — a rate, a receipt threshold.
func validateAboveZero(label string, v float64, maximum float64) string {
	if math.IsNaN(v) || v <= 0 {
		return label + " must be greater than zero"
	}
	return validateDecimal(label, v, 0, maximum)
}

// formatNumber renders a number in its shortest decimal form.
func formatNumber(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// pgDate stores a date as the date column wants it.
func pgDate(d time.Time) pgtype.Date { return pgtype.Date{Time: d, Valid: true} }

// numericFromFloat converts a JSON number into a numeric column value through
// its shortest round-tripping decimal text, so the column's own scale does the
// rounding rather than Go — the conversion projects and products make for their
// prices.
//
// Scan's error is returned rather than discarded: it can only fire for an
// infinity, which validation has already refused, and discarding it would store
// a silent NULL for a value that was actually given.
func numericFromFloat(v float64) (pgtype.Numeric, error) {
	return numericFromText(strconv.FormatFloat(v, 'f', -1, 64))
}

// numericFromFloatPtr is numericFromFloat for an optional value; nil stays an
// invalid (SQL NULL) pgtype.Numeric.
func numericFromFloatPtr(v *float64) (pgtype.Numeric, error) {
	if v == nil {
		return pgtype.Numeric{}, nil
	}
	return numericFromFloat(*v)
}

// numericFromText stores an exact decimal text as the column's value — how the
// seeded rates, which are written as text so no float ever rounds them, reach
// the table.
func numericFromText(text string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(text); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("expenses: %q is not a storable decimal: %w", text, err)
	}
	return n, nil
}

// floatFromNumeric reads a NOT NULL numeric column back onto the wire as a JSON
// number. A number the column holds but Go cannot read is an infrastructure
// failure, returned rather than rendered as zero.
func floatFromNumeric(n pgtype.Numeric) (float64, error) {
	v, err := floatPtrFromNumeric(n)
	if err != nil {
		return 0, err
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}

// floatPtrFromNumeric reads an optional numeric column back onto the wire as a
// JSON number, nil for a SQL NULL — never 0, which is a value somebody actually
// set to nothing.
func floatPtrFromNumeric(n pgtype.Numeric) (*float64, error) {
	if !n.Valid {
		return nil, nil
	}
	f, err := n.Float64Value()
	if err != nil {
		return nil, fmt.Errorf("expenses: read a stored decimal: %w", err)
	}
	if !f.Valid {
		return nil, nil
	}
	return &f.Float64, nil
}

// ratFromNumeric reads a numeric column as the exact decimal it holds — Int ×
// 10^Exp, never through a float — for the arithmetic that must not round twice
// (Global Constraints' exact decimals).
func ratFromNumeric(n pgtype.Numeric) (*big.Rat, error) {
	if !n.Valid || n.Int == nil {
		return nil, fmt.Errorf("expenses: read a stored decimal: the column holds no number")
	}
	if n.NaN || n.InfinityModifier != pgtype.Finite {
		return nil, fmt.Errorf("expenses: read a stored decimal: the column holds %v", n.InfinityModifier)
	}
	value := new(big.Rat).SetInt(n.Int)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs32(n.Exp))), nil)
	if n.Exp < 0 {
		return value.Quo(value, new(big.Rat).SetInt(scale)), nil
	}
	return value.Mul(value, new(big.Rat).SetInt(scale)), nil
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
