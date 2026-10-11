package staff

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/vantigo-io/vantigo/server/internal/staff/ident"
)

// This file is the module's value rules. Each takes the raw value and answers
// the normalised one plus the message to report, "" when the rule holds; a
// parse function runs them all and collects every failure, so a caller sees
// every problem with their request at once.

// The widths the columns hold, in characters — what varchar(n) counts.
const (
	structureNameMaxLength = 200
	costCentreMaxLength    = 40
	divisionMaxLength      = 100
	streetMaxLength        = 200
	postalCodeMaxLength    = 16
	cityMaxLength          = 100
	providerMaxLength      = 200
	employeeNumberMax      = 16
	prefixMaxLength        = 8
	departmentCodeMax      = 20
)

// numberPattern is the alphabet of every identifier HR types — an employee
// number's prefix, a department's code: letters a–z in either case, digits
// and the hyphen, which is what the a-melding, a payroll file and Entra's
// employeeId all carry without escaping.
var numberPattern = regexp.MustCompile(`^[A-Za-z0-9-]*$`)

// requiredText is a name: trimmed, at least one character, at most limit.
// label names the value in the message ("A name").
func requiredText(label, raw string, limit int) (string, string) {
	v := strings.TrimSpace(raw)
	switch n := utf8.RuneCountInString(v); {
	case n == 0:
		return "", label + " is required"
	case n > limit:
		return "", fmt.Sprintf("%s cannot be longer than %d characters, the given value was %d characters", label, limit, n)
	}
	return v, ""
}

// optionalText is an optional text: nil stays nil (the column cleared, since
// every write here is a full replace), anything else is trimmed and bounded. A
// value that is only whitespace is refused rather than stored as nothing:
// leaving the field out is how it is cleared, and a blank that silently
// clears would hide a client's mistake.
func optionalText(label string, raw *string, limit int) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	v := strings.TrimSpace(*raw)
	switch n := utf8.RuneCountInString(v); {
	case n == 0:
		return nil, label + " cannot be blank; leave it out to clear it"
	case n > limit:
		return nil, fmt.Sprintf("%s cannot be longer than %d characters, the given value was %d characters", label, limit, n)
	}
	return &v, ""
}

// parseCode is a department's code: 1 to 20 letters, digits or hyphens.
// Uniqueness, however it is cased, is the index's to decide.
func parseCode(raw string) (string, string) {
	v := strings.TrimSpace(raw)
	switch {
	case v == "":
		return "", "A code is required"
	case utf8.RuneCountInString(v) > departmentCodeMax:
		return "", fmt.Sprintf("A code cannot be longer than %d characters", departmentCodeMax)
	case !numberPattern.MatchString(v):
		return "", "A code may hold only letters a–z, digits and hyphens"
	}
	return v, ""
}

// parseCountry is an optional ISO 3166-1 alpha-2 country, upper-cased, and
// fallback when it is left out.
func parseCountry(raw *string, fallback string) (string, string) {
	if raw == nil {
		return fallback, ""
	}
	v := strings.ToUpper(strings.TrimSpace(*raw))
	if !validCountry(v) {
		return "", fmt.Sprintf("A country must be an ISO 3166-1 alpha-2 code, but was '%s'", *raw)
	}
	return v, ""
}

// parseOrgNumber is an optional organisation number: the spaces a person types
// between the groups taken out, then nine digits with a valid MOD11 check
// digit — staff/ident's check, the one every identifier of this module goes
// through. It answers the normalised number and the message.
func parseOrgNumber(raw *string) (*string, string) {
	if raw == nil {
		return nil, ""
	}
	// ident.ErrInvalidOrgNumber is the check's only refusal; its wrapped
	// reason stays out of the message, which says what a valid number is.
	v, err := ident.ValidateOrgNumber(*raw)
	if err != nil {
		return nil, "An organisation number is nine digits with a valid check digit"
	}
	return &v, ""
}

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

// listDefaultPageSize and listMaxPageSize are the paging bounds every other
// module's list uses; listMaxPage keeps page × pageSize inside the int32 the
// offset is sent as (Expenses' bound, internal/expenses/entries.go).
const (
	listDefaultPageSize = 25
	listMaxPageSize     = 100
	listMaxPage         = math.MaxInt32 / listMaxPageSize
)

// validatePageParams is the paging rule, every failure collected and named by
// its parameter.
func validatePageParams(page, pageSize *int32) fieldErrors {
	var f fieldErrors
	switch {
	case page == nil:
	case *page < 1:
		f.add("page", fmt.Sprintf("'page' must be 1 or greater, but was %d.", *page))
	case *page > listMaxPage:
		f.add("page", fmt.Sprintf("'page' must be at most %d, but was %d.", listMaxPage, *page))
	}
	if pageSize != nil && (*pageSize < 1 || *pageSize > listMaxPageSize) {
		f.add("pageSize", fmt.Sprintf("'pageSize' must be between 1 and %d, but was %d.", listMaxPageSize, *pageSize))
	}
	return f
}

// pageParams is the validated paging as numbers: page 1 and
// listDefaultPageSize rows unless the caller asked otherwise.
func pageParams(page, pageSize *int32) (int32, int32) {
	p, size := int32(1), int32(listDefaultPageSize)
	if page != nil {
		p = *page
	}
	if pageSize != nil {
		size = *pageSize
	}
	return p, size
}
