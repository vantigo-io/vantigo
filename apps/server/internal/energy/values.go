package energy

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/jackc/pgx/v5/pgtype"
)

// This file holds the module's small value-level helpers: duplicated from
// products/values.go and customers/customers.go where noted, because
// depguard forbids internal/energy/** from importing either.

// utf16Length duplicates products/values.go's same-named helper: .NET
// string.Length counts UTF-16 code units, not runes, so a length check
// against a DTO's declared max length must too.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// gsrnValid is Gsrn.IsValid (DM/MeteringPoints/Gsrn.cs:20-21): exactly 18
// ASCII digits — no GS1 check-digit validation despite the type's doc
// comment (energy inventory §2.1a).
func gsrnValid(value string) bool {
	if len(value) != 18 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// isNullOrValidGsrn is MeteringPointRequestExtensions.IsNullOrValidGsrn
// (MeteringPointRequest.cs:104): despite the name, a nil Gsrn does *not*
// pass — it behaves as "is present and valid" (energy inventory §2.1).
func isNullOrValidGsrn(value *string) bool {
	return value != nil && gsrnValid(*value)
}

// priceAreaValid is PriceArea.IsValid (DM/MeteringPoints/PriceArea.cs:5-9):
// two uppercase ASCII letters followed by one or two ASCII digits.
func priceAreaValid(value string) bool {
	if len(value) < 3 || len(value) > 4 {
		return false
	}
	if value[0] < 'A' || value[0] > 'Z' || value[1] < 'A' || value[1] > 'Z' {
		return false
	}
	for i := 2; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// connectionStatuses are MeteringPoint's ConnectionStatus enum members, in
// their canonical (PascalCase) wire form.
var connectionStatuses = []string{"New", "Connected", "Disconnected"}

// parseConnectionStatus is the DTOs' `Enum.TryParse<ConnectionStatus>(value, true, out _)`
// check plus the domain's own default (MeteringPointRequest.cs:36,
// ToDomain:50): nil means "New", matching is case-insensitive, but the
// canonical member name is always what gets stored, regardless of the
// input's own casing.
func parseConnectionStatus(value *string) (string, bool) {
	if value == nil {
		return "New", true
	}
	for _, s := range connectionStatuses {
		if strings.EqualFold(s, *value) {
			return s, true
		}
	}
	return "", false
}

// isAllLetters is char.IsLetter applied to every rune (Unicode letter
// categories, not just ASCII) — the DTO's own country-code shape check
// (MeteringPointRequest.cs:28).
func isAllLetters(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// likeReplacer/likePattern duplicate customers/customers.go's ILIKE
// escaping (EscapeLikePattern, GetMeteringPointsEndpoint.cs:35): depguard
// forbids this module importing customers, and the function is three
// lines.
var likeReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likePattern(search string) string {
	return "%" + likeReplacer.Replace(search) + "%"
}

// numericFromFloatPtr/floatPtrFromNumeric duplicate products/values.go's
// pgtype.Numeric<->float64 conversions (depguard forbids importing
// products): a nil pointer is SQL NULL both ways, and a non-nil value is
// formatted with its shortest round-tripping decimal text rather than
// rounded in Go — Postgres's numeric(14,3) column scale is where rounding
// happens (energy inventory §3.1 oddity 7), exactly as .NET relied on
// Postgres to do it.
func numericFromFloatPtr(v *float64) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	_ = n.Scan(strconv.FormatFloat(*v, 'f', -1, 64))
	return n
}

func floatPtrFromNumeric(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	return &f.Float64
}
